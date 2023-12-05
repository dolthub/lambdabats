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
	"archive/tar"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
)

func BuildTestsFile(doltSrcDir string) (string, error) {
	binDir := filepath.Join(os.TempDir(), uuid.New().String())
	defer os.RemoveAll(binDir)

	doltBinFilePath := filepath.Join(binDir, "dolt")
	err := RunWithSpinner("building dolt...", func() error {
		compileDolt := exec.Command("go")
		compileDolt.Args = []string{
			"go", "build", "-o", doltBinFilePath, "./cmd/dolt",
		}
		compileDolt.Dir = filepath.Join(doltSrcDir, "go")
		compileDolt.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64")
		out, err := compileDolt.CombinedOutput()
		if err != nil {
			return fmt.Errorf("error running go build dolt: %w\n%s", err, string(out))
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	remotesrvBinFilePath := filepath.Join(binDir, "remotesrv")
	err = RunWithSpinner("building remotesrv...", func() error {
		compileRemotesrv := exec.Command("go")
		compileRemotesrv.Args = []string{
			"go", "build", "-o", remotesrvBinFilePath, "./utils/remotesrv",
		}
		compileRemotesrv.Dir = filepath.Join(doltSrcDir, "go")
		compileRemotesrv.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64")
		err = compileRemotesrv.Run()
		if err != nil {
			return fmt.Errorf("error building remotesrv: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	batsTarFilePath := filepath.Join(os.TempDir(), uuid.New().String())
	defer os.RemoveAll(batsTarFilePath)

	err = RunWithSpinner("building bats.tar...", func() error {
		f, err := os.Create(batsTarFilePath)
		if err != nil {
			return err
		}
		defer f.Close()
		w := tar.NewWriter(f)
		defer w.Close()

		dfs := os.DirFS(filepath.Join(doltSrcDir, "integration-tests"))
		err = fs.WalkDir(dfs, "bats", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if d.IsDir() {
				return w.WriteHeader(&tar.Header{
					Name: path + "/",
					Mode: int64(fi.Mode()),
				})
			} else {
				return WriteFileToTar(w, &tar.Header{
					Name: path,
					Mode: int64(fi.Mode()),
				}, filepath.Join(filepath.Join(doltSrcDir, "integration-tests"), path))

			}
		})
		if err != nil {
			return fmt.Errorf("error taring up bats tests: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	testsTarPath := filepath.Join(os.TempDir(), uuid.New().String()+".tar")
	f, err := os.Create(testsTarPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	w := tar.NewWriter(f)
	defer w.Close()

	err = w.WriteHeader(&tar.Header{
		Name: "bin/",
		Mode: 0777,
	})

	err = WriteFileToTar(w, &tar.Header{
		Name: "bin/dolt",
		Mode: 0777,
	}, doltBinFilePath)
	if err != nil {
		return "", err
	}
	err = WriteFileToTar(w, &tar.Header{
		Name: "bin/remotesrv",
		Mode: 0777,
	}, remotesrvBinFilePath)
	if err != nil {
		return "", err
	}
	err = WriteFileToTar(w, &tar.Header{
		Name: "bats.tar",
		Mode: 0666,
	}, batsTarFilePath)
	if err != nil {
		return "", err
	}

	return testsTarPath, nil
}

func WriteFileToTar(w *tar.Writer, header *tar.Header, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	header.Size = stat.Size()
	if header.Mode == 0 {
		header.Mode = int64(stat.Mode())
	}
	err = w.WriteHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

func UploadTests(ctx context.Context, uploader Uploader, doltSrcDir string) (string, error) {
	testsTar, err := BuildTestsFile(doltSrcDir)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(testsTar)
	err = uploader.Upload(ctx, testsTar)
	if err != nil {
		return "", err
	}
	return filepath.Base(strings.TrimSuffix(testsTar, ".tar")), nil
}

type Uploader interface {
	Upload(ctx context.Context, path string) error
}

type CopyingUploader struct {
	dir string
}

func (c *CopyingUploader) Upload(ctx context.Context, testsPath string) error {
	destPath := filepath.Join(c.dir, filepath.Base(testsPath))
	src, err := os.Open(testsPath)
	if err != nil {
		return err
	}
	defer src.Close()
	dest, err := os.Create(destPath)
	defer dest.Close()
	if err != nil {
		return err
	}
	_, err = io.Copy(dest, src)

	// Sleep here to deal with macOS FUSE nonsense?
	time.Sleep(1 * time.Second)

	return err
}

type S3Uploader struct {
	s3client *s3.Client
	bucket   string
}

func NewS3Uploader(ctx context.Context, bucket string) (*S3Uploader, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	return &S3Uploader{
		s3client: s3.NewFromConfig(cfg),
		bucket:   bucket,
	}, nil
}

func (d *S3Uploader) Upload(ctx context.Context, path string) error {
	srcF, err := os.Open(path)
	if err != nil {
		return err
	}
	defer srcF.Close()
	fi, err := srcF.Stat()
	if err != nil {
		return err
	}
	bar := progressbar.DefaultBytes(fi.Size(), "uploading tests to s3")
	rd := NewProgressBarReader(srcF, bar)
	key := aws.String(filepath.Base(strings.TrimSuffix(path, ".tar")))
	bucket := aws.String(d.bucket)
	uploader := manager.NewUploader(d.s3client)
	_, err = uploader.Upload(ctx, &s3.PutObjectInput{
		Key:           key,
		Bucket:        bucket,
		Body:          rd,
		ContentLength: aws.Int64(fi.Size()),
	})
	return err
}
