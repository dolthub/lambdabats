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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sync"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"

	"github.com/reltuk/lambda-play/wire"
)

type SkipRunner struct {
	reason string
}

func NewSkipRunner(reason string) SkipRunner {
	return SkipRunner{reason: reason}
}

func (r SkipRunner) Run(ctx context.Context, req wire.RunTestRequest) (wire.RunTestResult, error) {
	return wire.RunTestResult{
		Output: SkippedJUnitTestCaseOutput(req.FileName, req.TestName, r.reason),
	}, nil
}

type LocalRunner struct {
	batsDir string

	// Only one test can run locally at a time.
	mu sync.Mutex
}

func NewLocalRunner(batsDir string) *LocalRunner {
	return &LocalRunner{batsDir: batsDir}
}

func (r *LocalRunner) Run(ctx context.Context, req wire.RunTestRequest) (wire.RunTestResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cmd := exec.Command("bats")
	cmd.Dir = r.batsDir
	cmd.Args = []string{
		"bats", "-F", "junit", "-f", req.TestFilter, req.FileName,
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return wire.RunTestResult{
			Output: string(output),
			Err:    err.Error(),
		}, nil
	}
	return wire.RunTestResult{
		Output: string(output),
	}, nil
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

func NewLambdaInvokeRunner(ctx context.Context, cfg aws.Config, function string) (*LambdaInvokeRunner, error) {
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
