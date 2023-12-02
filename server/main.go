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

	"github.com/reltuk/lambda-play/wire"
)

func handleRequest(ctx context.Context, request events.LambdaFunctionURLRequest) (events.LambdaFunctionURLResponse, error) {
	var testReq wire.RunTestRequest
	err := json.Unmarshal([]byte(request.Body), &testReq)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, err
	}
	if testReq.TestLocation == "" {
		return events.LambdaFunctionURLResponse{Body: "must supply test_location", StatusCode: 400}, nil
	}
	if testReq.FileName == "" {
		return events.LambdaFunctionURLResponse{Body: "must supply file_name", StatusCode: 400}, nil
	}
	if testReq.TestName == "" {
		return events.LambdaFunctionURLResponse{Body: "must supply test_name", StatusCode: 400}, nil
	}

	runLocation, err := UnpackTest(ctx, &CopyingDownloader{}, testReq.TestLocation)
	if err != nil {
		return events.LambdaFunctionURLResponse{}, err
	}

	var res wire.RunTestResult

	cmd := exec.Command("bats")
	if cmd.Err != nil {
		return events.LambdaFunctionURLResponse{}, cmd.Err
	}
	cmd.Dir = filepath.Join(runLocation, "bats")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "PATH="+filepath.Join(runLocation, "bin")+":"+os.Getenv("PATH"))
	cmd.Args = []string{
		"bats", "-F", "junit", "-f", testReq.TestName, testReq.FileName,
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

func NewS3Downloader(ctx context.Context, bucket string) *S3Downloader {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		panic(err)
	}
	return &S3Downloader{
		s3client: s3.NewFromConfig(cfg),
		bucket:   bucket,
	}
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

func UnpackTest(ctx context.Context, downloader Downloader, test string) (string, error) {
	testDir := filepath.Join(os.TempDir(), "downloaded_tests")
	destDir := filepath.Join(testDir, test)
	sentinelPath := filepath.Join(destDir, ".downloaded")
	f, err := os.Open(sentinelPath)
	if err == nil {
		f.Close()
		return destDir, nil
	}

	// If the setinel path doesn't exist, we create it.
	err = os.RemoveAll(destDir)
	if err != nil {
		return "", err
	}
	err = os.MkdirAll(destDir, 0777)
	if err != nil {
		return "", err
	}

	// Now we invoke the downloader to get the .tar file.
	tarDestPath := filepath.Join(destDir, test+".tar")
	err = downloader.Download(ctx, tarDestPath, test)
	if err != nil {
		return "", err
	}

	// Unpack the test tar file.
	untar := exec.Command("tar")
	untar.Args = []string{"tar", "xf", tarDestPath}
	untar.Dir = destDir
	out, err := untar.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("could not untar %s: %w\n\n%s", tarDestPath, err, string(out))
	}
	untar = exec.Command("tar")
	untar.Args = []string{"tar", "xf", "bats.tar"}
	untar.Dir = destDir
	err = untar.Run()
	if err != nil {
		return "", fmt.Errorf("could not untar bats.tar: %w", err)
	}

	// Touch the sentinel file.
	f, err = os.Create(sentinelPath)
	if err != nil {
		return "", fmt.Errorf("could not make sentine file %s: %w", sentinelPath, err)
	}
	f.Close()

	return destDir, nil
}

func main() {
	lambda.Start(handleRequest)
}
