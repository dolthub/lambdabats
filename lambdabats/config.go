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
	"errors"
	"os"
	"path/filepath"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
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

const AwsConfig = `
[default]
region = us-west-2

[profile corp_runner]
role_arn = arn:aws:iam::407903926827:role/RunBatsInLambda
region = us-west-2
source_profile = corp_sso_developer

[profile corp_sso_developer]
sso_session = dolthub_sso_session
sso_account_id = 407903926827
sso_role_name = DoltHubDeveloper
region = us-west-2

[sso-session dolthub_sso_session]
sso_start_url = https://d-90678b8781.awsapps.com/start#
sso_region = us-east-1
sso_registration_scopes = sso:account:access
`

func WithAWSConfig(cb func(path string) error) error {
	f, err := os.CreateTemp(os.TempDir(), "lambda-bats-aws-config-*")
	if err != nil {
		return err
	}
	configPath := f.Name()
	defer os.RemoveAll(f.Name())
	bs := []byte(AwsConfig)
	n, err := f.Write(bs)
	f.Close()
	if err != nil {
		return err
	}
	if n != len(bs) {
		return errors.New("short write writing config")
	}
	return cb(configPath)
}

func NewAWSRunConfig(ctx context.Context, envCreds bool) (RunConfig, error) {
	var cfg aws.Config
	var err error
	if envCreds {
		cfg, err = config.LoadDefaultConfig(ctx)
		if err != nil {
			return RunConfig{}, err
		}
	} else {
		err = WithAWSConfig(func(path string) error {
			cfg, err = config.LoadDefaultConfig(ctx,
				config.WithSharedConfigFiles([]string{path}),
				config.WithSharedConfigProfile("corp_runner"),
				config.WithRegion("us-west-2"),
				config.WithSharedCredentialsFiles(nil))
			return err
		})
		if err != nil {
			return RunConfig{}, err
		}
	}

	uploader, err := NewS3Uploader(ctx, cfg, S3BucketName)
	if err != nil {
		return RunConfig{}, err
	}
	runner, err := NewLambdaInvokeRunner(ctx, cfg, LambdaFunctionName)
	if err != nil {
		return RunConfig{}, err
	}
	return RunConfig{
		Concurrency: 512,
		Uploader:    uploader,
		Runner:      runner,
	}, nil
}
