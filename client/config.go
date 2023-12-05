package main

import (
	"context"
	"os"
	"path/filepath"
)

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
