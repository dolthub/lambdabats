// Copyright 2023 Dolthub, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/dolthub/lambdabats/wire"
)

func NewTestDownloader() (Downloader, error) {
	return &CopyingDownloader{}, nil
}

func handleRequest(ctx context.Context, request events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	var testReq wire.RunTestRequest
	err := json.Unmarshal([]byte(request.Body), &testReq)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, err
	}
	if testReq.DoltLocation == "" {
		return events.LambdaFunctionURLResponse{Body: "must supply dolt_location", StatusCode: 400}, nil
	}
	if testReq.BinLocation == "" {
		return events.LambdaFunctionURLResponse{Body: "must supply bin_location", StatusCode: 400}, nil
	}
	if testReq.BatsLocation == "" {
		return events.LambdaFunctionURLResponse{Body: "must supply bats_location", StatusCode: 400}, nil
	}
	if testReq.FileName == "" {
		return events.LambdaFunctionURLResponse{Body: "must supply file_name", StatusCode: 400}, nil
	}
	if testReq.TestName == "" {
		return events.LambdaFunctionURLResponse{Body: "must supply test_name", StatusCode: 400}, nil
	}
	if testReq.TestFilter == "" {
		return events.LambdaFunctionURLResponse{Body: "must supply test_filter", StatusCode: 400}, nil
	}

	downloader, err := NewTestDownloader()
	if _, ok := os.LookupEnv("USE_LOCAL_DOWNLOADER"); !ok {
		downloader, err = NewS3Downloader(ctx, "dolt-cloud-test-run-artifacts")
	}
	if err != nil {
		return events.LambdaFunctionURLResponse{}, err
	}

	runLocation, newPath, err := UnpackTest(ctx, downloader, testReq.DoltLocation, testReq.BinLocation, testReq.BatsLocation)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, err
	}

	var res wire.RunTestResult

	batsTempDir := filepath.Join(os.TempDir(), "bats_test_tmpdir")
	err = os.RemoveAll(batsTempDir)
	if err != nil {
		exec.Command("chmod", "-R", "777", batsTempDir).CombinedOutput()
		err = os.RemoveAll(batsTempDir)
		if err != nil {
			out, oerr := exec.Command("id").CombinedOutput()
			if oerr == nil {
				err = fmt.Errorf("id: %s, err: %w", out, err)
			} else {
				err = fmt.Errorf("id err: %w, err: %w", oerr, err)
			}
			out, lserr := exec.Command("ls", "-laRt", batsTempDir).CombinedOutput()
			if oerr == nil {
				err = fmt.Errorf("ls: %s, err: %w", out, err)
			} else {
				err = fmt.Errorf("ls err: %w, err: %w", lserr, err)
			}
			return events.LambdaFunctionURLResponse{}, err
		}
	}
	err = os.MkdirAll(batsTempDir, 0777)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, err
	}

	homeTempDir := filepath.Join(os.TempDir(), "bats_test_home")
	err = os.RemoveAll(homeTempDir)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, err
	}
	err = os.MkdirAll(homeTempDir, 0777)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, err
	}

	cmd := exec.Command("bats")
	if cmd.Err != nil {
		return events.LambdaFunctionURLResponse{}, cmd.Err
	}
	cmd.Dir = filepath.Join(runLocation, "bats")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, testReq.EnvVars...)
	cmd.Env = append(cmd.Env, "PATH="+newPath+":"+os.Getenv("PATH"))
	cmd.Env = append(cmd.Env, "TMPDIR="+batsTempDir)
	cmd.Env = append(cmd.Env, "HOME="+homeTempDir)
	cmd.Args = []string{
		"bats", "-F", "junit", "-f", testReq.TestFilter, testReq.FileName,
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		res.Err = err.Error()
	}
	res.Output = string(output)

	body, err := json.Marshal(res)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, err
	}

	return events.LambdaFunctionURLResponse{Body: string(body), StatusCode: 200}, nil
}

type Downloader interface {
	Download(ctx context.Context, dest, name string) error
}

func NewS3Downloader(ctx context.Context, bucket string) (*S3Downloader, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &S3Downloader{
		s3client: s3.NewFromConfig(cfg),
		bucket:   bucket,
	}, nil
}

type S3Downloader struct {
	s3client *s3.Client
	bucket   string
}

func (d *S3Downloader) Download(ctx context.Context, dest, name string) error {
	key := aws.String(name)
	bucket := aws.String(d.bucket)
	resp, err := d.s3client.GetObject(ctx, &s3.GetObjectInput{
		Key:    key,
		Bucket: bucket,
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	destF, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destF.Close()
	_, err = io.Copy(destF, resp.Body)
	return err
}

type CopyingDownloader struct {
}

func (d *CopyingDownloader) Download(ctx context.Context, dest, name string) error {
	uploadPath := filepath.Join("/test_uploads", name+".tar")
	src, err := os.Open(uploadPath)
	if err != nil {
		return err
	}
	defer src.Close()
	destF, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer destF.Close()
	_, err = io.Copy(destF, src)
	return err
}

func UnpackTest(ctx context.Context, downloader Downloader, dolt, bin, test string) (string, string, error) {
	testDir := filepath.Join(os.TempDir(), "downloaded_tests")
	testDestDir := filepath.Join(testDir, test)
	err := DownloadAndUntar(ctx, downloader, testDestDir, test)
	if err != nil {
		return "", "", err
	}

	binDir := filepath.Join(os.TempDir(), "downloaded_bins")
	binDestDir := filepath.Join(binDir, bin)
	err = DownloadAndUntar(ctx, downloader, binDestDir, bin)
	if err != nil {
		return "", "", err
	}

	doltDir := filepath.Join(os.TempDir(), "downloaded_dolts")
	doltDestDir := filepath.Join(doltDir, dolt)
	err = DownloadAndUntar(ctx, downloader, doltDestDir, dolt)
	if err != nil {
		return "", "", err
	}

	return testDestDir, filepath.Join(doltDestDir, "bin") + ":" + filepath.Join(binDestDir, "bin"), nil
}

func DownloadAndUntar(ctx context.Context, downloader Downloader, dest, name string) error {
	sentinelPath := filepath.Join(dest, ".downloaded")
	f, err := os.Open(sentinelPath)
	if err == nil {
		f.Close()
		return nil
	}

	// If the setinel path doesn't exist, we create it.
	// XXX: A bit gross here...
	err = os.RemoveAll(filepath.Join(dest, ".."))
	if err != nil {
		return err
	}
	err = os.MkdirAll(dest, 0777)
	if err != nil {
		return err
	}

	// Now we invoke the downloader to get the .tar file.
	tarDestPath := filepath.Join(dest, name+".tar")
	err = downloader.Download(ctx, tarDestPath, name)
	if err != nil {
		return err
	}

	untar := exec.Command("tar")
	untar.Args = []string{"tar", "xf", tarDestPath}
	untar.Dir = dest
	out, err := untar.CombinedOutput()
	if err != nil {
		return fmt.Errorf("could not untar %s: %w\n\n%s", tarDestPath, err, string(out))
	}

	// Touch the sentinel file.
	f, err = os.Create(sentinelPath)
	if err != nil {
		return fmt.Errorf("could not make sentine file %s: %w", sentinelPath, err)
	}
	f.Close()

	return nil
}

func main() {
	lambda.Start(handleRequest)
}
