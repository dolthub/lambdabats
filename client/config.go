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
