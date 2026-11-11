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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/fatih/color"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dolthub/lambdabats/wire"
)

// Payloads of failed invocations, as seen in Dolt CI runs.
const (
	eofPayload      = `{"errorMessage":"unexpected EOF","errorType":"errorString"}`
	timeoutPayload  = `{"errorType":"Sandbox.Timedout","errorMessage":"RequestId: ffc9744a-23bc-4702-ba9e-2fee564bf5ca Error: Task timed out after 45.00 seconds"}`
	resetPayload    = `{"errorMessage":"read tcp 169.254.100.6:58600->16.15.34.54:443: read: connection reset by peer","errorType":"OpError"}`
	deadlinePayload = `{"errorMessage":"context deadline exceeded","errorType":"deadlineExceededError"}`
	noSuchKey       = `{"errorMessage":"operation error S3: GetObject, https response error StatusCode: 404, api error NoSuchKey: The specified key does not exist.","errorType":"OperationError"}`
)

func passingJUnitOutput(name string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<testsuites time="1">
<testsuite name="f.bats" tests="1" failures="0" errors="0" skipped="0" time="1">
    <testcase classname="f.bats" name="` + name + `" time="1" />
</testsuite>
</testsuites>
`
}

func failingJUnitOutput(name string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<testsuites time="1">
<testsuite name="f.bats" tests="1" failures="1" errors="0" skipped="0" time="1">
    <testcase classname="f.bats" name="` + name + `" time="1">
        <failure type="failure">it failed</failure>
    </testcase>
</testsuite>
</testsuites>
`
}

func urlResponsePayload(t *testing.T, statusCode int, res wire.RunTestResult) []byte {
	body, err := json.Marshal(res)
	require.NoError(t, err)
	bs, err := json.Marshal(events.LambdaFunctionURLResponse{StatusCode: statusCode, Body: string(body)})
	require.NoError(t, err)
	return bs
}

func TestParseLambdaFunctionError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		payload   string
		errType   string
		transient bool
	}{
		{"unexpected EOF", eofPayload, "errorString", true},
		{"timeout", timeoutPayload, "Sandbox.Timedout", true},
		{"connection reset", resetPayload, "OpError", true},
		{"deadline exceeded", deadlinePayload, "deadlineExceededError", true},
		{"missing artifact", noSuchKey, "OperationError", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fnErr := ParseLambdaFunctionError([]byte(tc.payload))
			require.NotNil(t, fnErr)
			assert.Equal(t, tc.errType, fnErr.Type)
			assert.Equal(t, tc.transient, fnErr.Transient())
		})
	}

	t.Run("function URL response", func(t *testing.T) {
		assert.Nil(t, ParseLambdaFunctionError(urlResponsePayload(t, 200, wire.RunTestResult{Output: "out"})))
		assert.Nil(t, ParseLambdaFunctionError(urlResponsePayload(t, 400, wire.RunTestResult{})))
	})
	t.Run("not JSON", func(t *testing.T) {
		assert.Nil(t, ParseLambdaFunctionError([]byte("not json")))
	})
}

func TestFromLambdaFunctionURLHTTPReResponseBytes(t *testing.T) {
	_, err := FromLambdaFunctionURLHTTPReResponseBytes([]byte(timeoutPayload))
	var fnErr *LambdaFunctionError
	require.ErrorAs(t, err, &fnErr)
	assert.Equal(t, "Sandbox.Timedout", fnErr.Type)

	res, err := FromLambdaFunctionURLHTTPReResponseBytes(urlResponsePayload(t, 200, wire.RunTestResult{Output: "out", Err: "exit status 1"}))
	require.NoError(t, err)
	assert.Equal(t, wire.RunTestResult{Output: "out", Err: "exit status 1"}, res)

	res, err = FromLambdaFunctionURLHTTPReResponseBytes(urlResponsePayload(t, 400, wire.RunTestResult{}))
	require.NoError(t, err)
	assert.Contains(t, res.Err, "non-200 status code")
}

func TestLambdaEmulatorRunnerFunctionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, eofPayload)
	}))
	defer srv.Close()
	runner := &LambdaEmulatorRunner{endpointURL: srv.URL}
	_, err := runner.Run(context.Background(), wire.RunTestRequest{})
	var fnErr *LambdaFunctionError
	require.ErrorAs(t, err, &fnErr)
	assert.True(t, fnErr.Transient())
}

func TestLambdaInvokeRunnerFunctionError(t *testing.T) {
	var payload, functionError string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if functionError != "" {
			w.Header().Set("X-Amz-Function-Error", functionError)
		}
		io.WriteString(w, payload)
	}))
	defer srv.Close()
	runner := &LambdaInvokeRunner{
		function: "f",
		client: lambda.New(lambda.Options{
			Region:       "us-west-2",
			BaseEndpoint: aws.String(srv.URL),
			Credentials:  aws.AnonymousCredentials{},
		}),
	}
	ctx := context.Background()

	payload, functionError = timeoutPayload, "Unhandled"
	_, err := runner.Run(ctx, wire.RunTestRequest{})
	var fnErr *LambdaFunctionError
	require.ErrorAs(t, err, &fnErr)
	assert.Equal(t, "Sandbox.Timedout", fnErr.Type)
	assert.True(t, fnErr.Transient())

	payload, functionError = "not json", "Unhandled"
	_, err = runner.Run(ctx, wire.RunTestRequest{})
	require.ErrorAs(t, err, &fnErr)
	assert.Equal(t, "Unhandled", fnErr.Type)
	assert.Equal(t, "not json", fnErr.Message)
	assert.False(t, fnErr.Transient())

	payload, functionError = string(urlResponsePayload(t, 200, wire.RunTestResult{Output: "out"})), ""
	res, err := runner.Run(ctx, wire.RunTestRequest{})
	require.NoError(t, err)
	assert.Equal(t, "out", res.Output)
}

type fakeResult struct {
	res wire.RunTestResult
	err error
}

// fakeRunner returns |results| in order, repeating the last one.
type fakeRunner struct {
	results []fakeResult
	calls   int
}

func (f *fakeRunner) Run(ctx context.Context, req wire.RunTestRequest) (wire.RunTestResult, error) {
	r := f.results[min(f.calls, len(f.results)-1)]
	f.calls++
	return r.res, r.err
}

func noBackoff(t *testing.T) {
	saved := RetryBackoff
	RetryBackoff = func(int) time.Duration { return 0 }
	t.Cleanup(func() { RetryBackoff = saved })
}

func TestRunWithRetries(t *testing.T) {
	noBackoff(t)
	ctx := context.Background()
	const name = "f: a test"
	passed := fakeResult{res: wire.RunTestResult{Output: passingJUnitOutput(name)}}
	failed := fakeResult{res: wire.RunTestResult{Output: failingJUnitOutput(name), Err: "exit status 1"}}
	transient := fakeResult{err: ParseLambdaFunctionError([]byte(eofPayload))}
	permanent := fakeResult{err: ParseLambdaFunctionError([]byte(noSuchKey))}

	t.Run("passes after transient errors", func(t *testing.T) {
		r := &fakeRunner{results: []fakeResult{transient, {err: ParseLambdaFunctionError([]byte(timeoutPayload))}, passed}}
		runs, err := RunWithRetries(ctx, r, wire.RunTestRequest{}, 2)
		require.NoError(t, err)
		require.Len(t, runs, 3)
		test := Test{Name: name, Runs: runs}
		res, err := test.FinalRun().Result(name)
		require.NoError(t, err)
		assert.Equal(t, TestRunResultStatus(TestRunResultStatus_Success), res.Status)
		retries := test.Retries()
		require.Len(t, retries, 2)
		assert.Equal(t, "errorString", retries[0].Type)
		assert.Equal(t, "Sandbox.Timedout", retries[1].Type)
	})

	t.Run("gives up after retries", func(t *testing.T) {
		r := &fakeRunner{results: []fakeResult{transient}}
		runs, err := RunWithRetries(ctx, r, wire.RunTestRequest{}, 2)
		require.NoError(t, err)
		assert.Equal(t, 3, r.calls)
		require.Len(t, runs, 3)
		final := runs[len(runs)-1]
		require.NotNil(t, final.FunctionError)
		_, err = final.Result(name)
		assert.ErrorContains(t, err, "unexpected EOF")
	})

	t.Run("retries disabled", func(t *testing.T) {
		r := &fakeRunner{results: []fakeResult{transient, passed}}
		runs, err := RunWithRetries(ctx, r, wire.RunTestRequest{}, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, r.calls)
		require.Len(t, runs, 1)
		assert.NotNil(t, runs[0].FunctionError)
	})

	t.Run("does not retry permanent function errors", func(t *testing.T) {
		r := &fakeRunner{results: []fakeResult{permanent, passed}}
		runs, err := RunWithRetries(ctx, r, wire.RunTestRequest{}, 2)
		require.NoError(t, err)
		assert.Equal(t, 1, r.calls)
		require.Len(t, runs, 1)
		_, err = runs[0].Result(name)
		assert.ErrorContains(t, err, "NoSuchKey")
	})

	t.Run("does not retry test failures", func(t *testing.T) {
		r := &fakeRunner{results: []fakeResult{failed, passed}}
		runs, err := RunWithRetries(ctx, r, wire.RunTestRequest{}, 2)
		require.NoError(t, err)
		assert.Equal(t, 1, r.calls)
		require.Len(t, runs, 1)
		res, err := runs[0].Result(name)
		require.NoError(t, err)
		assert.Equal(t, TestRunResultStatus(TestRunResultStatus_Failure), res.Status)
	})

	t.Run("returns other errors", func(t *testing.T) {
		boom := errors.New("boom")
		r := &fakeRunner{results: []fakeResult{{err: boom}, passed}}
		_, err := RunWithRetries(ctx, r, wire.RunTestRequest{}, 2)
		assert.ErrorIs(t, err, boom)
		assert.Equal(t, 1, r.calls)
	})
}

func TestRunWithRetriesCanceledDuringBackoff(t *testing.T) {
	saved := RetryBackoff
	RetryBackoff = func(int) time.Duration { return time.Hour }
	t.Cleanup(func() { RetryBackoff = saved })

	ctx, cancel := context.WithCancel(context.Background())
	r := &fakeRunner{results: []fakeResult{{err: ParseLambdaFunctionError([]byte(eofPayload))}}}
	time.AfterFunc(10*time.Millisecond, cancel)
	_, err := RunWithRetries(ctx, r, wire.RunTestRequest{}, 2)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, r.calls)
}

func TestRetryBackoff(t *testing.T) {
	for attempt := range 3 {
		base := (4 * time.Second) << attempt
		for range 100 {
			d := RetryBackoff(attempt)
			assert.GreaterOrEqual(t, d, base/2)
			assert.Less(t, d, base/2+base)
		}
	}
}

func captureStdout(t *testing.T, f func()) string {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	saved, savedColor := os.Stdout, color.Output
	os.Stdout, color.Output = w, w
	done := make(chan string)
	go func() {
		bs, _ := io.ReadAll(r)
		done <- string(bs)
	}()
	f()
	os.Stdout, color.Output = saved, savedColor
	w.Close()
	return <-done
}

func TestOutputTAPResultsWithRetries(t *testing.T) {
	timeoutErr := ParseLambdaFunctionError([]byte(timeoutPayload))
	eofErr := ParseLambdaFunctionError([]byte(eofPayload))
	fnRun := func(e *LambdaFunctionError) TestRun {
		return TestRun{Response: wire.RunTestResult{Err: e.Error()}, FunctionError: e}
	}
	files := []TestFile{{
		Name: "f.bats",
		Tests: []Test{
			{Name: "f: fatal", Runs: []TestRun{fnRun(eofErr), fnRun(eofErr)}},
			{Name: "f: retried", Runs: []TestRun{fnRun(timeoutErr), {Response: wire.RunTestResult{Output: passingJUnitOutput("f: retried")}}}},
			{Name: "f: passed", Runs: []TestRun{{Response: wire.RunTestResult{Output: passingJUnitOutput("f: passed")}}}},
		},
	}}
	var res int
	out := captureStdout(t, func() { res = OutputTAPResults(files) })
	assert.Equal(t, 1, res)
	lines := strings.Split(out, "\n")
	assert.Equal(t, "1..3", lines[0])
	assert.Equal(t, "not ok 1 f: fatal", lines[1])
	assert.Equal(t, "# retried after transient "+eofErr.Error(), lines[2])
	assert.Equal(t, "#"+eofErr.Error(), lines[3])
	assert.Contains(t, out, "\nok 2 f: retried\n# retried after transient "+timeoutErr.Error()+"\n")
	assert.Contains(t, out, "\nok 3 f: passed\n")
}

func TestOutputBatsResultsWithRetries(t *testing.T) {
	timeoutErr := ParseLambdaFunctionError([]byte(timeoutPayload))
	files := []TestFile{{
		Name: "f.bats",
		Tests: []Test{
			{Name: "f: retried", Runs: []TestRun{
				{Response: wire.RunTestResult{Err: timeoutErr.Error()}, FunctionError: timeoutErr},
				{Response: wire.RunTestResult{Output: passingJUnitOutput("f: retried")}},
			}},
		},
	}}
	var res int
	out := captureStdout(t, func() { res = OutputBatsResults(files) })
	assert.Equal(t, 0, res)
	assert.Contains(t, out, "f.bats 100% PASSED")
	assert.Contains(t, out, "1 tests retried after transient Lambda function errors:\n  f: retried: "+timeoutErr.Error())
	assert.Contains(t, out, "1 tests, 0 failures, 0 skipped, 1 retried")
}
