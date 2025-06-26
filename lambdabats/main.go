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
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"

	"github.com/dolthub/lambdabats/wire"
)

const S3BucketName = "dolt-cloud-test-run-artifacts"
const LambdaFunctionName = "dolt_bats_test_runner"

var OutputResults = OutputBatsResults

var OutputFormat = flag.String("F", "pretty", "format the test results output; either bats pretty format or tap")
var ExecutionStrategy = flag.String("s", "lambda", "execution strategy;\n  lambda - run most tests remote, some locally;\n  lambda_skip - run most tests remote, skip others;\n  lambda_emulator - run all tests against a local lambda simulator")
var EnvCreds = flag.Bool("use-aws-environment-credentials", false, "by default we use hard-coded credentials which work for DoltHub developers; this uses credentials from the environment instead.")
var TargetArch = flag.String("arch", "arm64", "target architecture for the lambda function; either amd64 or arm64")
var BuildOnly = flag.Bool("build-only", false, "Print the location of the test artifacts and exit without running the tests.")

var EnvVars []string

func PrintUsage() {
	fmt.Println("usage: lambda-bats [-F pretty|tap] [-s lambda|lambda_skip|lambda_emulator] BATS_DIR_OR_FILES...")
	fmt.Println("usage: lambda-bats login [--headless] - SSO login to AWS as a developer. Must have AWS CLI installed.")
	os.Exit(1)
}

func GetDoltSrcDir(args []string) (string, error) {
	path := args[0]
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}

	var doltDirPath string
	if fi.IsDir() {
		doltDirPath = filepath.Join(path, "../../")
	} else {
		doltDirPath = filepath.Join(filepath.Dir(path), "../../")
	}

	d, err := os.Open(doltDirPath)
	if err != nil {
		return "", err
	}
	defer d.Close()
	fi, err = d.Stat()
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("could not find dolt src directory from first file argument: %s", path)
	}
	return doltDirPath, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "login" {
		var useDeviceCode bool
		loginArgs := os.Args[2:]
		for _, arg := range loginArgs {
			if arg == "--headless" {
				useDeviceCode = true
			}
		}
		os.Exit(DoLogin(useDeviceCode))
	}

	flag.Func("env", "environment variable to set in the remote invocation; for example -env SQL_ENGINE=remote-engine", func(val string) error {
		if !strings.Contains(val, "=") {
			return fmt.Errorf("error, expected -env value to contain a variable setting such as ENVVAR=VALUE, got: %v", val)
		}
		EnvVars = append(EnvVars, val)
		return nil
	})

	flag.Parse()

	if *OutputFormat != "pretty" && *OutputFormat != "tap" {
		fmt.Println("invalid output format")
		PrintUsage()
	} else if *OutputFormat == "tap" {
		OutputResults = OutputTAPResults
	}
	if *ExecutionStrategy != "lambda" && *ExecutionStrategy != "lambda_skip" && *ExecutionStrategy != "lambda_emulator" {
		fmt.Println("invalid execution strategy")
		PrintUsage()
	}
	if *TargetArch != "arm64" && *TargetArch != "amd64" {
		fmt.Println("invalid target architecture")
		PrintUsage()
	}

	if *TargetArch == "amd64" {
		fmt.Println("Forcing --build-only because x86 is not supported")
		*BuildOnly = true
	}

	fileArgs := flag.Args()
	if len(fileArgs) == 0 {
		fmt.Println("must supply tests to run")
		PrintUsage()
	}

	doltSrcDir, err := GetDoltSrcDir(fileArgs)
	if err != nil {
		fmt.Printf("could not find dolt source directory: %v\n", err)
		PrintUsage()
	}

	ctx := context.Background()

	var config RunConfig
	var fallbackRunner Runner

	switch *ExecutionStrategy {
	case "lambda":
		config, err = NewAWSRunConfig(ctx, *EnvCreds)
		if err != nil {
			panic(err)
		}
		fallbackRunner = NewLocalRunner(filepath.Join(doltSrcDir, "integration-tests/bats"))
	case "lambda_skip":
		config, err = NewAWSRunConfig(ctx, *EnvCreds)
		if err != nil {
			panic(err)
		}
		fallbackRunner = NewSkipRunner("lambda runner does not support virtual ttys")
	case "lambda_emulator":
		config = NewTestRunConfig()
	}

	testArtifacts, err := UploadTests(ctx, config.Uploader, doltSrcDir, *TargetArch, *BuildOnly)
	if err != nil {
		panic(err)
	}

	if *BuildOnly {
		fmt.Println("Test artifacts saved. Exiting.")
		os.Exit(0)
	}

	files, total, err := LoadTestFiles(fileArgs)
	if err != nil {
		panic(err)
	}

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(config.Concurrency)
	bar := progressbar.Default(int64(total), "running tests")

	RunTest := func(fi, ti int) {
		eg.Go(func() error {
			filter := EscapeNameForFilter(files[fi].Tests[ti].Name)
			req := wire.RunTestRequest{
				DoltLocation: testArtifacts.DoltPath,
				BinLocation:  testArtifacts.BinPath,
				BatsLocation: testArtifacts.TestsPath,
				FileName:     files[fi].Name,
				TestName:     files[fi].Tests[ti].Name,
				TestFilter:   filter,
				EnvVars:      EnvVars,
			}
			runner := config.Runner
			if files[fi].Tests[ti].HasTag("no_lambda") {
				runner = fallbackRunner
			}
			resp, err := runner.Run(egCtx, req)
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
	res := OutputResults(files)
	os.Exit(res)
}

func DoLogin(useDeviceCode bool) int {
	err := WithAWSConfig(func(path string) error {
		cmd := exec.Command("aws")
		args := []string{
			"aws", "sso", "login", "--sso-session", "dolthub_sso_session",
		}
		if useDeviceCode {
			args = append(args, "--use-device-code")
		}
		cmd.Args = args
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = os.Environ()
		cmd.Env = append(cmd.Env, "AWS_CONFIG_FILE="+path)
		err := cmd.Run()
		return err
	})
	if err != nil {
		fmt.Printf("error running `aws sso login`: %v", err)
		return 1
	}
	return 0
}
