// Copyright 2026 Dolthub, Inc.
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
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/dolthub/lambdabats/wire"
)

// LambdaFunctionError is returned by a Runner when the Lambda invocation
// itself failed, as opposed to bats running and reporting a test result.
// This happens when the handler returns an error, which it only does when
// setting up the test run fails (for example, downloading the test artifacts
// from S3), or when the Lambda runtime kills the invocation (for example,
// because it timed out).
type LambdaFunctionError struct {
	Type    string
	Message string
}

func (e *LambdaFunctionError) Error() string {
	return fmt.Sprintf("lambda function error: %s: %s", e.Type, e.Message)
}

// transientFunctionErrorMessages are substrings of LambdaFunctionError
// messages which indicate a failure that is likely to succeed on retry. These
// come from the handler's S3 downloads failing partway through or running out
// of time.
var transientFunctionErrorMessages = []string{
	"unexpected EOF",
	"connection reset by peer",
	"context deadline exceeded",
}

// Transient returns true if the invocation failed in a way that is likely to
// succeed on retry. This is an allowlist so that persistent setup failures,
// such as a missing test artifact, are not retried for every test.
func (e *LambdaFunctionError) Transient() bool {
	if e.Type == "Sandbox.Timedout" {
		return true
	}
	for _, s := range transientFunctionErrorMessages {
		if strings.Contains(e.Message, s) {
			return true
		}
	}
	return false
}

// ParseLambdaFunctionError returns a *LambdaFunctionError if |payload| is the
// payload Lambda returns for a failed invocation, which looks like
// {"errorType": "...", "errorMessage": "..."}, and nil otherwise.
func ParseLambdaFunctionError(payload []byte) *LambdaFunctionError {
	var p struct {
		StatusCode   int    `json:"statusCode"`
		ErrorType    string `json:"errorType"`
		ErrorMessage string `json:"errorMessage"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil
	}
	if p.StatusCode != 0 || (p.ErrorType == "" && p.ErrorMessage == "") {
		return nil
	}
	return &LambdaFunctionError{Type: p.ErrorType, Message: p.ErrorMessage}
}

// RetryBackoff returns how long to wait before retry number |attempt|,
// counting from zero. Transient failures tend to arrive in bursts across many
// concurrent invocations, so wait a few seconds, with jitter, rather than
// retrying immediately.
var RetryBackoff = func(attempt int) time.Duration {
	base := (4 * time.Second) << attempt
	return base/2 + rand.N(base)
}

// RunWithRetries runs |req| with |runner|, retrying up to |retries| times
// when the invocation fails with a transient LambdaFunctionError. Test
// failures reported by bats are never retried. It returns one TestRun per
// attempt; the last one is the result of the test.
//
// A LambdaFunctionError which is not retried is recorded in the returned
// TestRun, so that it is reported as a fatal error for that test. Any other
// error from |runner| is returned.
func RunWithRetries(ctx context.Context, runner Runner, req wire.RunTestRequest, retries int) ([]TestRun, error) {
	var runs []TestRun
	for attempt := 0; ; attempt++ {
		resp, err := runner.Run(ctx, req)
		var fnErr *LambdaFunctionError
		if !errors.As(err, &fnErr) {
			if err != nil {
				return nil, err
			}
			return append(runs, TestRun{Response: resp}), nil
		}
		runs = append(runs, TestRun{
			Response:      wire.RunTestResult{Err: fnErr.Error()},
			FunctionError: fnErr,
		})
		if !fnErr.Transient() || attempt >= retries {
			return runs, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(RetryBackoff(attempt)):
		}
	}
}
