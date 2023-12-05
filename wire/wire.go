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
	TestLocation string `json:"test_location"`
	FileName     string `json:"file_name"`
	TestName     string `json:"test_name"`
	TestFilter   string `json:"test_filter"`
}

type RunTestResult struct {
	Output string `json:"output"`
	Err    string `json:"err"`
}
