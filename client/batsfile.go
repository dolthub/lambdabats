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
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"path/filepath"

	"github.com/reltuk/lambda-play/wire"
)

type TestFile struct {
	Name  string
	Tests []Test
}

type Test struct {
	Name string
	Tags []string
	Runs []TestRun
	File TestFile
}

func (t Test) HasTag(tag string) bool {
	for _, t := range t.Tags {
		if tag == t {
			return true
		}
	}
	return false
}

type TestRun struct {
	Response wire.RunTestResult
}

type TestRunResultStatus int

const (
	TestRunResultStatus_Success = iota
	TestRunResultStatus_Failure
	TestRunResultStatus_Skipped
)

type TestRunResult struct {
	Status TestRunResultStatus
	Output string
}

func (tr TestRun) Result(name string) (TestRunResult, error) {
	type TestCase struct {
		Name    string  `xml:"name,attr"`
		Skipped *string `xml:"skipped"`
		Failure *string `xml:"failure"`
	}
	type TestSuite struct {
		TestCases []TestCase `xml:"testcase"`
	}
	type JUnitReport struct {
		XMLName    xml.Name    `xml:"testsuites"`
		TestSuites []TestSuite `xml:"testsuite"`
	}

	if tr.Response.Err != "" && tr.Response.Err != "exit status 1" {
		return TestRunResult{}, errors.New(tr.Response.Err)
	}

	var unmarshaled JUnitReport
	err := xml.Unmarshal([]byte(tr.Response.Output), &unmarshaled)
	if err != nil {
		return TestRunResult{}, nil
	}
	if len(unmarshaled.TestSuites) != 1 {
		return TestRunResult{}, errors.New("expected one testsuites element")
	}
	var tc *TestCase
	for _, v := range unmarshaled.TestSuites[0].TestCases {
		if v.Name == name {
			tc = &v
			break
		}
	}
	if tc == nil {
		return TestRunResult{}, fmt.Errorf("expected to find a testcase element with name \"%s\"", name)
	}
	if tc.Skipped != nil {
		return TestRunResult{Status: TestRunResultStatus_Skipped, Output: *tc.Skipped}, nil
	}
	if tc.Failure != nil {
		return TestRunResult{Status: TestRunResultStatus_Failure, Output: *tc.Failure}, nil
	}
	return TestRunResult{Status: TestRunResultStatus_Success}, nil
}

func SkippedJUnitTestCaseOutput(filename, testname, reason string) string {
	return `
<?xml version="1.0" encoding="UTF-8"?>
<testsuites time="0">
<testsuite name="` + filename + `" tests="1" failures="0" errors="0" skipped="1" time="0">
    <testcase classname="` + filename + `" name="` + testname + `" time="0">
       <skipped>` + reason + `</skipped>
    </testcase>
</testsuite>
</testsuites>
`
}

// Read the *.bats files in a directory and collect the tests found in them.
func LoadTestFiles(args []string) ([]TestFile, int, error) {
	numTests := 0
	var files []TestFile

	var fileSys fs.FS
	var batsDir string

	for _, arg := range args {
		f, err := os.Open(arg)
		if err != nil {
			return nil, 0, err
		}
		fi, err := f.Stat()
		f.Close()
		if err != nil {
			return nil, 0, err
		}
		if fi.IsDir() {
			if batsDir == "" {
				batsDir = arg
			} else if batsDir != arg {
				return nil, 0, errors.New("error loading test files: all bats files must be in a single directory")
			}
			fileSys = os.DirFS(arg)
			entries, err := fs.ReadDir(fileSys, ".")
			if err != nil {
				return nil, 0, err
			}
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".bats") {
					files = append(files, TestFile{Name: e.Name()})
				}
			}
		} else {
			if batsDir == "" {
				batsDir = filepath.Dir(arg)
			} else if batsDir != filepath.Dir(arg) {
				return nil, 0, errors.New("error loading test files: all bats files must be in a single directory")
			}
			if fileSys == nil {
				fileSys = os.DirFS(batsDir)
			}
			files = append(files, TestFile{Name: filepath.Base(arg)})
		}
	}

	for i := range files {
		var err error
		files[i].Tests, err = LoadTests(fileSys, files[i])
		if err != nil {
			return nil, 0, err
		}
		numTests += len(files[i].Tests)
	}

	return files, numTests, nil
}

func LoadTests(fileSys fs.FS, tf TestFile) ([]Test, error) {
	f, err := fileSys.Open(tf.Name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var tags []string
	var res []Test
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "# bats test_tags=") {
			line = strings.TrimPrefix(line, "# bats test_tags=")
			tags = strings.Split(line, " ")
		} else if strings.HasPrefix(line, "@test \"") {
			line = strings.TrimPrefix(line, "@test \"")
			line = strings.TrimRight(line, "\" {")
			res = append(res, Test{Name: line, Tags: tags, File: tf})
			tags = nil
		}
	}
	return res, s.Err()
}

func EscapeNameForFilter(n string) string {
	escaped := strings.ReplaceAll(n, "(", "\\(")
	escaped = strings.ReplaceAll(escaped, "+", "\\+")
	return "^" + escaped + "$"
}
