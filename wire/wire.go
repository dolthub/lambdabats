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

package wire

type RunTestRequest struct {
	// The name of the uploaded file which contains the tarball with
	// bats/*.
	BatsLocation string `json:"bats_location"`

	// The name of the uploaded file which contains the tarball with test
	// uploaded bin/* files. Currently remotesrv.
	BinLocation string `json:"bin_location"`

	// The name of the uploaded file which contains the tarball with
	// bin/dolt.
	DoltLocation string `json:"dolt_location"`

	// The test file run, for example, sql-server.bats.
	FileName string `json:"file_name"`

	// The test name within the file to run.
	TestName string `json:"test_name"`

	// The filter string to pass to the `bats` invocation to actually run
	// the targetted test. This is an escaped version of the test_name.
	TestFilter string `json:"test_filter"`
}

type RunTestResult struct {
	Output string `json:"output"`
	Err    string `json:"err"`
}
