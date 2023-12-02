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
	"github.com/fatih/color"
	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"

	"github.com/reltuk/lambda-play/wire"
)

type RunConfig struct {
	Concurrency int
	Uploader    Uploader
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
	}
}

type TestFile struct {
	Name  string
	Tests []Test
}

type Test struct {
	Name string
	Runs []TestRun
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

func (tr TestRun) Result() (TestRunResult, error) {
	type Skipped struct {
	}
	type TestCase struct {
		Skipped *Skipped `xml:"skipped"`
		Failure *string  `xml:"failure"`
	}
	type TestSuite struct {
		TestCases []TestCase `xml:"testcase"`
	}
	type JUnitReport struct {
		XMLName    xml.Name    `xml:"testsuites"`
		TestSuites []TestSuite `xml:"testsuite"`
	}

	if tr.Response.Err != "" {
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
	if len(unmarshaled.TestSuites[0].TestCases) != 1 {
		return TestRunResult{}, errors.New("expected one testcases element")
	}
	tc := unmarshaled.TestSuites[0].TestCases[0]
	if tc.Skipped != nil {
		return TestRunResult{Status: TestRunResultStatus_Skipped}, nil
	}
	if tc.Failure != nil {
		return TestRunResult{Status: TestRunResultStatus_Failure, Output: *tc.Failure}, nil
	}
	return TestRunResult{Status: TestRunResultStatus_Success}, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: client DIR_NAME_WITH_DOLT_SRC")
		os.Exit(1)
	}

	doltSrcDir := os.Args[1]

	ctx := context.Background()

	config := NewTestRunConfig()
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

	runner := NewLambdaEmulatorRunner()
	RunTest := func(fi, ti int) {
		eg.Go(func() error {
			resp, err := runner.Run(egCtx, wire.RunTestRequest{
				TestLocation: testLocation,
				FileName:     files[fi].Name,
				TestName:     files[fi].Tests[ti].Name,
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

	blue := color.New(color.FgBlue)
	red := color.New(color.FgRed)

	// Print the results...
	numTests := 0
	numSkipped := 0
	numFailed := 0
	for _, f := range files {
		blue.Println(f.Name)
		for _, t := range f.Tests {
			numTests += 1
			res, err := t.Runs[0].Result()
			if err != nil {
				panic(err)
			}
			if res.Status == TestRunResultStatus_Success {
				fmt.Printf("  ✓ %s\n", t.Name)
			} else if res.Status == TestRunResultStatus_Skipped {
				numSkipped += 1
				fmt.Printf("  - %s (skipped)\n", t.Name)
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
	if numFailed > 0 {
		red.Printf("%d tests, %d failures, %d skipped\n", numTests, numFailed, numSkipped)
	} else {
		fmt.Printf("%d tests, %d failures, %d skipped\n", numTests, numFailed, numSkipped)
	}
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
		if strings.HasSuffix(e.Name(), ".bats") && (e.Name() == "ls.bats" || e.Name() == "sql-server-remotesrv.bats") {
			files = append(files, TestFile{Name: e.Name()})
		}
	}

	for i := range files {
		files[i].Tests, err = LoadTests(fileSys, files[i].Name)
		if err != nil {
			return nil, 0, err
		}
		numTests += len(files[i].Tests)
	}

	return files, numTests, nil
}

func LoadTests(fileSys fs.FS, filename string) ([]Test, error) {
	f, err := fileSys.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var res []Test
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "@test \"") {
			line = strings.TrimPrefix(line, "@test \"")
			line = strings.TrimRight(line, "\" {")
			res = append(res, Test{Name: line})
		}
	}
	return res, s.Err()
}

func BuildTestsFile(doltSrcDir string) (string, error) {
	doltBinFilePath := filepath.Join(os.TempDir(), uuid.New().String())
	compileDolt := exec.Command("go")
	compileDolt.Args = []string{
		"go", "build", "-o", doltBinFilePath, "./cmd/dolt",
	}
	compileDolt.Dir = filepath.Join(doltSrcDir, "go")
	compileDolt.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64")
	out, err := compileDolt.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("error running go build dolt: %w\n%s", err, string(out))
	}

	remotesrvBinFilePath := filepath.Join(os.TempDir(), uuid.New().String())
	compileRemotesrv := exec.Command("go")
	compileRemotesrv.Args = []string{
		"go", "build", "-o", remotesrvBinFilePath, "./utils/remotesrv",
	}
	compileRemotesrv.Dir = filepath.Join(doltSrcDir, "go")
	compileRemotesrv.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64")
	err = compileRemotesrv.Run()
	if err != nil {
		return "", fmt.Errorf("error building remotesrv: %w", err)
	}

	batsTarFilePath := filepath.Join(os.TempDir(), uuid.New().String())
	tarBatsFiles := exec.Command("tar")
	tarBatsFiles.Args = []string{
		"tar", "cf", batsTarFilePath, "-C", "integration-tests", "bats",
	}
	tarBatsFiles.Dir = doltSrcDir
	err = tarBatsFiles.Run()
	if err != nil {
		return "", fmt.Errorf("error taring up bats tests: %w", err)
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

func UploadTests(ctx context.Context, uploader Uploader, doltSrcDir string) (string, error) {
	testsTar, err := BuildTestsFile(doltSrcDir)
	if err != nil {
		return "", err
	}
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
	bodyBytes, err := json.Marshal(req)
	if err != nil {
		return res, err
	}
	lambdaReq := events.LambdaFunctionURLRequest{
		Version: "2.0",
		RawPath: "/",
		Body:    string(bodyBytes),
	}
	bodyBytes, err = json.Marshal(lambdaReq)
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
	var lambdaResp events.LambdaFunctionURLResponse
	err = json.Unmarshal(bodyBytes, &lambdaResp)
	if err != nil {
		return res, err
	}
	if lambdaResp.StatusCode != 200 {
		return res, fmt.Errorf("non-200 status code in lambda response: code: %d, body: %s", lambdaResp.StatusCode, lambdaResp.Body)
	}
	err = json.Unmarshal([]byte(lambdaResp.Body), &res)
	if err != nil {
		return res, err
	}
	return res, nil
}
