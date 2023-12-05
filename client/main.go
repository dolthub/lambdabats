package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/fatih/color"
	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"

	"github.com/reltuk/lambda-play/wire"
)

const S3BucketName = "dolt-cloud-test-run-artifacts"
const LambdaFunctionName = "dolt_bats_test_runner"

type RunConfig struct {
	Concurrency int
	Uploader    Uploader
	Runner      Runner
}

func NewTestRunConfig() RunConfig {
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	uploader := &CopyingUploader{dir: filepath.Join(wd, "../docker/uploads")}
	if err != nil {
		panic(err)
	}
	return RunConfig{
		Concurrency: 1,
		Uploader:    uploader,
		Runner:      NewLambdaEmulatorRunner(),
	}
}

func NewAWSRunConfig(ctx context.Context) (RunConfig, error) {
	uploader, err := NewS3Uploader(ctx, S3BucketName)
	if err != nil {
		return RunConfig{}, err
	}
	runner, err := NewLambdaInvokeRunner(ctx, LambdaFunctionName)
	if err != nil {
		return RunConfig{}, err
	}
	return RunConfig{
		Concurrency: 512,
		Uploader:    uploader,
		Runner:      runner,
	}, nil
}

type TestFile struct {
	Name  string
	Tests []Test
}

type Test struct {
	Name string
	Tags []string
	Runs []TestRun
	File TestFile
}

func (t Test) HasTag(tag string) bool {
	for _, t := range t.Tags {
		if tag == t {
			return true
		}
	}
	return false
}

type TestRun struct {
	Response wire.RunTestResult
}

type TestRunResultStatus int

const (
	TestRunResultStatus_Success = iota
	TestRunResultStatus_Failure
	TestRunResultStatus_Skipped
)

type TestRunResult struct {
	Status TestRunResultStatus
	Output string
}

func (tr TestRun) Result(name string) (TestRunResult, error) {
	type TestCase struct {
		Name    string  `xml:"name,attr"`
		Skipped *string `xml:"skipped"`
		Failure *string `xml:"failure"`
	}
	type TestSuite struct {
		TestCases []TestCase `xml:"testcase"`
	}
	type JUnitReport struct {
		XMLName    xml.Name    `xml:"testsuites"`
		TestSuites []TestSuite `xml:"testsuite"`
	}

	if tr.Response.Err != "" && tr.Response.Err != "exit status 1" {
		return TestRunResult{}, errors.New(tr.Response.Err)
	}

	var unmarshaled JUnitReport
	err := xml.Unmarshal([]byte(tr.Response.Output), &unmarshaled)
	if err != nil {
		return TestRunResult{}, nil
	}
	if len(unmarshaled.TestSuites) != 1 {
		return TestRunResult{}, errors.New("expected one testsuites element")
	}
	var tc *TestCase
	for _, v := range unmarshaled.TestSuites[0].TestCases {
		if v.Name == name {
			tc = &v
			break
		}
	}
	if tc == nil {
		return TestRunResult{}, fmt.Errorf("expected to find a testcase element with name \"%s\"", name)
	}
	if tc.Skipped != nil {
		return TestRunResult{Status: TestRunResultStatus_Skipped, Output: *tc.Skipped}, nil
	}
	if tc.Failure != nil {
		return TestRunResult{Status: TestRunResultStatus_Failure, Output: *tc.Failure}, nil
	}
	return TestRunResult{Status: TestRunResultStatus_Success}, nil
}

func SkippedJUnitTestCaseOutput(filename, testname, reason string) string {
	return `
<?xml version="1.0" encoding="UTF-8"?>
<testsuites time="0">
<testsuite name="` + filename + `" tests="1" failures="0" errors="0" skipped="1" time="0">
    <testcase classname="` + filename + `" name="` + testname + `" time="0">
       <skipped>` + reason + `</skipped>
    </testcase>
</testsuite>
</testsuites>
`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: client DIR_NAME_WITH_DOLT_SRC")
		os.Exit(1)
	}

	doltSrcDir := os.Args[1]

	ctx := context.Background()

	config := NewTestRunConfig()
	if _, ok := os.LookupEnv("RUN_AGAINST_LAMBDA"); ok {
		var err error
		config, err = NewAWSRunConfig(ctx)
		if err != nil {
			panic(err)
		}
	}

	testLocation, err := UploadTests(ctx, config.Uploader, doltSrcDir)
	if err != nil {
		panic(err)
	}

	files, total, err := LoadTestFiles(filepath.Join(doltSrcDir, "integration-tests/bats"))
	if err != nil {
		panic(err)
	}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(config.Concurrency)
	bar := progressbar.Default(int64(total), "running tests")

	RunTest := func(fi, ti int) {
		eg.Go(func() error {
			if files[fi].Tests[ti].HasTag("no_lambda") {
				bar.Add(1)
				files[fi].Tests[ti].Runs = append(files[fi].Tests[ti].Runs, TestRun{
					Response: wire.RunTestResult{
						Output: SkippedJUnitTestCaseOutput(files[fi].Name, files[fi].Tests[ti].Name, "lambda runner does not support virtual ttys"),
					},
				})
				return nil
			}
			filter := EscapeNameForFilter(files[fi].Tests[ti].Name)
			resp, err := config.Runner.Run(egCtx, wire.RunTestRequest{
				TestLocation: testLocation,
				FileName:     files[fi].Name,
				TestName:     filter,
			})
			if err != nil {
				return err
			}
			bar.Add(1)
			files[fi].Tests[ti].Runs = append(files[fi].Tests[ti].Runs, TestRun{
				Response: resp,
			})
			return nil
		})
	}

	// Run all the tests...
	for fi := range files {
		for ti := range files[fi].Tests {
			RunTest(fi, ti)
		}
	}
	err = eg.Wait()
	if err != nil {
		panic(err)
	}
	bar.Finish()
	bar.Close()

	// Print the results...
	res := OutputTAPResults(files)
	os.Exit(res)
}

func OutputBatsResults(files []TestFile) int {
	blue := color.New(color.FgBlue)
	red := color.New(color.FgRed)

	numTests := 0
	numSkipped := 0
	numFailed := 0
	numFatal := 0
	for _, f := range files {
		blue.Println(f.Name)
		for _, t := range f.Tests {
			numTests += 1
			res, err := t.Runs[0].Result(t.Name)
			if err != nil {
				numFatal += 1
				red.Printf("  ✗ %s\n", t.Name)
				for _, line := range strings.Split(t.Runs[0].Response.Err, "\n") {
					red.Printf("  %s\n", line)
				}
				for _, line := range strings.Split(t.Runs[0].Response.Output, "\n") {
					red.Printf("  %s\n", line)
				}
				continue
			}
			if res.Status == TestRunResultStatus_Success {
				fmt.Printf("  ✓ %s\n", t.Name)
			} else if res.Status == TestRunResultStatus_Skipped {
				numSkipped += 1
				if res.Output == "" {
					fmt.Printf("  - %s (skipped)\n", t.Name)
				} else {
					fmt.Printf("  - %s (skipped: %s)\n", t.Name, res.Output)
				}
			} else {
				numFailed += 1
				red.Printf("  ✗ %s\n", t.Name)
				for _, line := range strings.Split(res.Output, "\n") {
					red.Printf("  %s\n", line)
				}
			}
		}
		fmt.Println()
	}
	if numFatal > 0 {
		red.Printf("%d tests, %d fatal, %d failures, %d skipped\n", numTests, numFatal, numFailed, numSkipped)
	} else if numFailed > 0 {
		red.Printf("%d tests, %d failures, %d skipped\n", numTests, numFailed, numSkipped)
	} else {
		fmt.Printf("%d tests, %d failures, %d skipped\n", numTests, numFailed, numSkipped)
	}

	if numFailed == 0 && numFatal == 0 {
		return 0
	}
	return 1
}

func OutputTAPResults(files []TestFile) int {
	numTests := 0
	numFailed := 0
	numFatal := 0
	for _, f := range files {
		numTests += len(f.Tests)
	}
	fmt.Printf("1..%d\n", numTests)
	i := 1
	for _, f := range files {
		for _, t := range f.Tests {
			res, err := t.Runs[0].Result(t.Name)
			if err != nil {
				numFatal += 1
				fmt.Printf("not ok %d %s\n", i, t.Name)
				for _, line := range strings.Split(t.Runs[0].Response.Err, "\n") {
					fmt.Printf("#%s\n", line)
				}
				for _, line := range strings.Split(t.Runs[0].Response.Output, "\n") {
					fmt.Printf("#%s\n", line)
				}
				continue
			}
			if res.Status == TestRunResultStatus_Success {
				fmt.Printf("ok %d %s\n", i, t.Name)
			} else if res.Status == TestRunResultStatus_Skipped {
				if res.Output == "" {
					fmt.Printf("ok %d %s # skip\n", i, t.Name)
				} else {
					fmt.Printf("ok %d %s # skip %s\n", i, t.Name, res.Output)
				}
			} else {
				numFailed += 1
				fmt.Printf("not ok %d %s\n", i, t.Name)
				for _, line := range strings.Split(res.Output, "\n") {
					fmt.Printf("#%s\n", line)
				}
			}
			i += 1
		}
	}

	if numFailed == 0 && numFatal == 0 {
		return 0
	}
	return 1
}

// Read the *.bats files in a directory and collect the tests found in them.
func LoadTestFiles(dir string) ([]TestFile, int, error) {
	fileSys := os.DirFS(dir)
	entries, err := fs.ReadDir(fileSys, ".")
	if err != nil {
		return nil, 0, err
	}
	numTests := 0
	var files []TestFile
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".bats") {
			files = append(files, TestFile{Name: e.Name()})
		}
	}

	for i := range files {
		files[i].Tests, err = LoadTests(fileSys, files[i])
		if err != nil {
			return nil, 0, err
		}
		numTests += len(files[i].Tests)
	}

	return files, numTests, nil
}

func LoadTests(fileSys fs.FS, tf TestFile) ([]Test, error) {
	f, err := fileSys.Open(tf.Name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var tags []string
	var res []Test
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "# bats test_tags=") {
			line = strings.TrimPrefix(line, "# bats test_tags=")
			tags = strings.Split(line, " ")
		} else if strings.HasPrefix(line, "@test \"") {
			line = strings.TrimPrefix(line, "@test \"")
			line = strings.TrimRight(line, "\" {")
			res = append(res, Test{Name: line, Tags: tags, File: tf})
			tags = nil
		}
	}
	return res, s.Err()
}

func RunWithSpinner(message string, work func() error) error {
	done := make(chan struct{})
	var eg errgroup.Group
	eg.Go(func() error {
		defer close(done)
		return work()
	})
	eg.Go(func() error {
		i := 0
		spinner := []byte{'|', '/', '-', '\\'}
		fmt.Printf("%s %c", message, spinner[i])
		for {
			select {
			case <-done:
				fmt.Printf("\bdone\n")
				return nil
			case <-time.After(100 * time.Millisecond):
				i += 1
				if i == len(spinner) {
					i = 0
				}
				fmt.Printf("\b%c", spinner[i])
			}
		}
		return nil
	})
	return eg.Wait()
}

func BuildTestsFile(doltSrcDir string) (string, error) {
	binDir := filepath.Join(os.TempDir(), uuid.New().String())
	defer os.RemoveAll(binDir)

	doltBinFilePath := filepath.Join(binDir, "dolt")
	err := RunWithSpinner("building dolt...", func() error {
		compileDolt := exec.Command("go")
		compileDolt.Args = []string{
			"go", "build", "-o", doltBinFilePath, "./cmd/dolt",
		}
		compileDolt.Dir = filepath.Join(doltSrcDir, "go")
		compileDolt.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64")
		out, err := compileDolt.CombinedOutput()
		if err != nil {
			return fmt.Errorf("error running go build dolt: %w\n%s", err, string(out))
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	remotesrvBinFilePath := filepath.Join(binDir, "remotesrv")
	err = RunWithSpinner("building remotesrv...", func() error {
		compileRemotesrv := exec.Command("go")
		compileRemotesrv.Args = []string{
			"go", "build", "-o", remotesrvBinFilePath, "./utils/remotesrv",
		}
		compileRemotesrv.Dir = filepath.Join(doltSrcDir, "go")
		compileRemotesrv.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64")
		err = compileRemotesrv.Run()
		if err != nil {
			return fmt.Errorf("error building remotesrv: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	batsTarFilePath := filepath.Join(os.TempDir(), uuid.New().String())
	defer os.RemoveAll(batsTarFilePath)

	err = RunWithSpinner("building bats.tar...", func() error {
		f, err := os.Create(batsTarFilePath)
		if err != nil {
			return err
		}
		defer f.Close()
		w := tar.NewWriter(f)
		defer w.Close()

		dfs := os.DirFS(filepath.Join(doltSrcDir, "integration-tests"))
		err = fs.WalkDir(dfs, "bats", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if d.IsDir() {
				return w.WriteHeader(&tar.Header{
					Name: path + "/",
					Mode: int64(fi.Mode()),
				})
			} else {
				return WriteFileToTar(w, &tar.Header{
					Name: path,
					Mode: int64(fi.Mode()),
				}, filepath.Join(filepath.Join(doltSrcDir, "integration-tests"), path))

			}
		})
		if err != nil {
			return fmt.Errorf("error taring up bats tests: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	testsTarPath := filepath.Join(os.TempDir(), uuid.New().String()+".tar")
	f, err := os.Create(testsTarPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	w := tar.NewWriter(f)
	defer w.Close()

	err = w.WriteHeader(&tar.Header{
		Name: "bin/",
		Mode: 0777,
	})

	err = WriteFileToTar(w, &tar.Header{
		Name: "bin/dolt",
		Mode: 0777,
	}, doltBinFilePath)
	if err != nil {
		return "", err
	}
	err = WriteFileToTar(w, &tar.Header{
		Name: "bin/remotesrv",
		Mode: 0777,
	}, remotesrvBinFilePath)
	if err != nil {
		return "", err
	}
	err = WriteFileToTar(w, &tar.Header{
		Name: "bats.tar",
		Mode: 0666,
	}, batsTarFilePath)
	if err != nil {
		return "", err
	}

	return testsTarPath, nil
}

type Uploader interface {
	Upload(ctx context.Context, path string) error
}

type CopyingUploader struct {
	dir string
}

func (c *CopyingUploader) Upload(ctx context.Context, testsPath string) error {
	destPath := filepath.Join(c.dir, filepath.Base(testsPath))
	src, err := os.Open(testsPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dest, err := os.Create(destPath)
	defer dest.Close()
	if err != nil {
		return err
	}
	_, err = io.Copy(dest, src)

	// Sleep here to deal with macOS FUSE nonsense?
	time.Sleep(1 * time.Second)

	return err
}

func NewS3Uploader(ctx context.Context, bucket string) (*S3Uploader, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &S3Uploader{
		s3client: s3.NewFromConfig(cfg),
		bucket:   bucket,
	}, nil
}

type S3Uploader struct {
	s3client *s3.Client
	bucket   string
}

func (d *S3Uploader) Upload(ctx context.Context, path string) error {
	srcF, err := os.Open(path)
	if err != nil {
		return err
	}
	defer srcF.Close()
	fi, err := srcF.Stat()
	if err != nil {
		return err
	}
	bar := progressbar.DefaultBytes(fi.Size(), "uploading tests to s3")
	rd := NewProgressBarReader(srcF, bar)
	key := aws.String(filepath.Base(strings.TrimSuffix(path, ".tar")))
	bucket := aws.String(d.bucket)
	uploader := manager.NewUploader(d.s3client)
	_, err = uploader.Upload(ctx, &s3.PutObjectInput{
		Key:           key,
		Bucket:        bucket,
		Body:          rd,
		ContentLength: aws.Int64(fi.Size()),
	})
	return err
}

type ProgressBarReader struct {
	f   *os.File
	bar *progressbar.ProgressBar
}

func NewProgressBarReader(f *os.File, bar *progressbar.ProgressBar) *ProgressBarReader {
	return &ProgressBarReader{f, bar}
}

func (r *ProgressBarReader) Read(p []byte) (n int, err error) {
	n, err = r.f.Read(p)
	r.bar.Add(n)
	return
}

func (r *ProgressBarReader) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = r.f.ReadAt(p, off)
	r.bar.Add(n)
	return
}

func (r *ProgressBarReader) Seek(offset int64, whence int) (int64, error) {
	return r.f.Seek(offset, whence)
}

func (r *ProgressBarReader) Close() error {
	r.bar.Finish()
	return r.f.Close()
}

func UploadTests(ctx context.Context, uploader Uploader, doltSrcDir string) (string, error) {
	testsTar, err := BuildTestsFile(doltSrcDir)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(testsTar)
	err = uploader.Upload(ctx, testsTar)
	if err != nil {
		return "", err
	}
	return filepath.Base(strings.TrimSuffix(testsTar, ".tar")), nil
}

func WriteFileToTar(w *tar.Writer, header *tar.Header, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	header.Size = stat.Size()
	if header.Mode == 0 {
		header.Mode = int64(stat.Mode())
	}
	err = w.WriteHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

type Runner interface {
	Run(ctx context.Context, req wire.RunTestRequest) (wire.RunTestResult, error)
}

// A runner which calls our local lambda emulator.
type LambdaEmulatorRunner struct {
	endpointURL string
}

var _ Runner = (*LambdaEmulatorRunner)(nil)

func NewLambdaEmulatorRunner() *LambdaEmulatorRunner {
	return &LambdaEmulatorRunner{
		endpointURL: "http://localhost:8080/2015-03-31/functions/function/invocations",
	}
}

func (e *LambdaEmulatorRunner) Run(ctx context.Context, req wire.RunTestRequest) (wire.RunTestResult, error) {
	var res wire.RunTestResult
	bodyBytes, err := ToLambdaFunctionURLHTTPRequestBytes(req)
	if err != nil {
		return res, err
	}
	bodyReader := bytes.NewBuffer(bodyBytes)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", e.endpointURL, bodyReader)
	if err != nil {
		return res, err
	}
	httpReq.Header.Add("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(httpReq)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		return res, err
	}
	bodyBytes, err = io.ReadAll(resp.Body)
	if err != nil {
		return res, err
	}
	return FromLambdaFunctionURLHTTPReResponseBytes(bodyBytes)
}

// A runner which calls Invoke on a Lambda function with a FunctionURL event payload.
type LambdaInvokeRunner struct {
	function string
	client   *lambda.Client
}

var _ Runner = (*LambdaInvokeRunner)(nil)

func NewLambdaInvokeRunner(ctx context.Context, function string) (*LambdaInvokeRunner, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &LambdaInvokeRunner{
		function: function,
		client:   lambda.NewFromConfig(cfg),
	}, nil
}

func (e *LambdaInvokeRunner) Run(ctx context.Context, req wire.RunTestRequest) (wire.RunTestResult, error) {
	var res wire.RunTestResult
	bodyBytes, err := ToLambdaFunctionURLHTTPRequestBytes(req)
	if err != nil {
		return res, err
	}
	resp, err := e.client.Invoke(ctx, &lambda.InvokeInput{
		FunctionName: aws.String(e.function),
		Payload:      bodyBytes,
	})
	if err != nil {
		return res, err
	}
	return FromLambdaFunctionURLHTTPReResponseBytes(resp.Payload)
}

func ToLambdaFunctionURLHTTPRequestBytes(req wire.RunTestRequest) ([]byte, error) {
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	lambdaReq := events.LambdaFunctionURLRequest{
		Version: "2.0",
		RawPath: "/",
		Body:    string(bodyBytes),
	}
	return json.Marshal(lambdaReq)
}

func FromLambdaFunctionURLHTTPReResponseBytes(bs []byte) (wire.RunTestResult, error) {
	var res wire.RunTestResult
	var lambdaResp events.LambdaFunctionURLResponse
	err := json.Unmarshal(bs, &lambdaResp)
	if err != nil {
		return res, err
	}
	if lambdaResp.StatusCode != 200 {
		res.Err = fmt.Sprintf("non-200 status code in lambda response: code: %d, body: %s", lambdaResp.StatusCode, string(bs))
		return res, nil
	}
	err = json.Unmarshal([]byte(lambdaResp.Body), &res)
	return res, err
}

func EscapeNameForFilter(n string) string {
	escaped := strings.ReplaceAll(n, "(", "\\(")
	escaped = strings.ReplaceAll(escaped, "+", "\\+")
	return "^" + escaped + "$"
}
