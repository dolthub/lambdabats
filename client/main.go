package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"

	"github.com/reltuk/lambda-play/wire"
)

const S3BucketName = "dolt-cloud-test-run-artifacts"
const LambdaFunctionName = "dolt_bats_test_runner"

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

	var fallbackRunner Runner = NewSkipRunner("lambda runner does not support virtual ttys")
	fallbackRunner = NewLocalRunner(filepath.Join(doltSrcDir, "integration-tests/bats"))

	eg, egCtx := errgroup.WithContext(ctx)
	eg.SetLimit(config.Concurrency)
	bar := progressbar.Default(int64(total), "running tests")

	RunTest := func(fi, ti int) {
		eg.Go(func() error {
			filter := EscapeNameForFilter(files[fi].Tests[ti].Name)
			req := wire.RunTestRequest{
				TestLocation: testLocation,
				FileName:     files[fi].Name,
				TestName:     files[fi].Tests[ti].Name,
				TestFilter:   filter,
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
	res := OutputBatsResults(files)
	os.Exit(res)
}
