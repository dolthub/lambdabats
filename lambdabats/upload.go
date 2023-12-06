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
	"crypto/sha256"
	"encoding/base32"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"
)

func BuildTestsFile(doltSrcDir string) (UploadArtifacts, error) {
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
		return UploadArtifacts{}, err
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
		return UploadArtifacts{}, err
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
		return UploadArtifacts{}, err
	}

	doltHash := sha256.New()
	f, err := os.Open(doltBinFilePath)
	if err != nil {
		return UploadArtifacts{}, err
	}
	_, err = io.Copy(doltHash, f)
	f.Close()
	if err != nil {
		return UploadArtifacts{}, err
	}
	doltHashStr := base32.HexEncoding.EncodeToString(doltHash.Sum(nil))

	doltTarPath := filepath.Join(os.TempDir(), doltHashStr+".tar")
	err = func() error {
		f, err := os.Create(doltTarPath)
		if err != nil {
			return err
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
			return err
		}
		return nil
	}()
	if err != nil {
		return UploadArtifacts{}, err
	}

	binHash := sha256.New()
	f, err = os.Open(remotesrvBinFilePath)
	if err != nil {
		return UploadArtifacts{}, err
	}
	_, err = io.Copy(binHash, f)
	f.Close()
	if err != nil {
		return UploadArtifacts{}, err
	}
	binHashStr := base32.HexEncoding.EncodeToString(binHash.Sum(nil))

	binTarPath := filepath.Join(os.TempDir(), binHashStr+".tar")
	err = func() error {
		f, err := os.Create(binTarPath)
		if err != nil {
			return err
		}
		defer f.Close()
		w := tar.NewWriter(f)
		defer w.Close()
		err = w.WriteHeader(&tar.Header{
			Name: "bin/",
			Mode: 0777,
		})
		err = WriteFileToTar(w, &tar.Header{
			Name: "bin/remotesrv",
			Mode: 0777,
		}, remotesrvBinFilePath)
		if err != nil {
			return err
		}
		return nil
	}()
	if err != nil {
		return UploadArtifacts{}, err
	}

	batsHash := sha256.New()
	f, err = os.Open(batsTarFilePath)
	if err != nil {
		return UploadArtifacts{}, err
	}
	_, err = io.Copy(batsHash, f)
	f.Close()
	if err != nil {
		return UploadArtifacts{}, err
	}
	batsHashStr := base32.HexEncoding.EncodeToString(batsHash.Sum(nil))

	batsTarPath := filepath.Join(os.TempDir(), batsHashStr+".tar")
	err = func() error {
		f, err := os.Create(batsTarPath)
		if err != nil {
			return err
		}
		defer f.Close()
		src, err := os.Open(batsTarFilePath)
		if err != nil {
			return err
		}
		defer src.Close()
		_, err = io.Copy(f, src)
		return err
	}()

	return UploadArtifacts{
		DoltTarPath:  doltTarPath,
		BinTarPath:   binTarPath,
		TestsTarPath: batsTarPath,
	}, nil
}

type UploadArtifacts struct {
	DoltTarPath  string
	BinTarPath   string
	TestsTarPath string
}

type UploadLocations struct {
	DoltPath  string
	BinPath   string
	TestsPath string
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

func UploadTests(ctx context.Context, uploader Uploader, doltSrcDir string) (UploadLocations, error) {
	artifacts, err := BuildTestsFile(doltSrcDir)
	if err != nil {
		return UploadLocations{}, err
	}
	defer os.RemoveAll(artifacts.DoltTarPath)
	defer os.RemoveAll(artifacts.BinTarPath)
	defer os.RemoveAll(artifacts.TestsTarPath)
	err = uploader.Upload(ctx, artifacts)
	if err != nil {
		return UploadLocations{}, err
	}
	return UploadLocations{
		DoltPath:  filepath.Base(strings.TrimSuffix(artifacts.DoltTarPath, ".tar")),
		BinPath:   filepath.Base(strings.TrimSuffix(artifacts.BinTarPath, ".tar")),
		TestsPath: filepath.Base(strings.TrimSuffix(artifacts.TestsTarPath, ".tar")),
	}, nil
}

type Uploader interface {
	Upload(ctx context.Context, artifacts UploadArtifacts) error
}

type CopyingUploader struct {
	dir string
}

func (c *CopyingUploader) Upload(ctx context.Context, artifacts UploadArtifacts) error {
	for _, path := range []string{artifacts.DoltTarPath, artifacts.BinTarPath, artifacts.TestsTarPath} {
		destPath := filepath.Join(c.dir, filepath.Base(path))
		src, err := os.Open(path)
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
		if err != nil {
			return err
		}
	}

	// Sleep here to deal with macOS FUSE nonsense?
	time.Sleep(1 * time.Second)

	return nil
}

type S3Uploader struct {
	s3client *s3.Client
	bucket   string
}

func NewS3Uploader(ctx context.Context, cfg aws.Config, bucket string) (*S3Uploader, error) {
	return &S3Uploader{
		s3client: s3.NewFromConfig(cfg),
		bucket:   bucket,
	}, nil
}

func (d *S3Uploader) Upload(ctx context.Context, artifacts UploadArtifacts) error {
	doltF, err := os.Open(artifacts.DoltTarPath)
	if err != nil {
		return err
	}
	defer doltF.Close()
	fi, err := doltF.Stat()
	if err != nil {
		return err
	}
	doltSize := fi.Size()

	binF, err := os.Open(artifacts.BinTarPath)
	if err != nil {
		return err
	}
	defer binF.Close()
	fi, err = binF.Stat()
	if err != nil {
		return err
	}
	binSize := fi.Size()

	testsF, err := os.Open(artifacts.TestsTarPath)
	if err != nil {
		return err
	}
	defer testsF.Close()
	fi, err = testsF.Stat()
	if err != nil {
		return err
	}
	testsSize := fi.Size()

	size := doltSize + binSize + testsSize

	bar := progressbar.DefaultBytes(size, "uploading tests to s3")

	uploader := manager.NewUploader(d.s3client)

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		key := aws.String(filepath.Base(strings.TrimSuffix(artifacts.DoltTarPath, ".tar")))
		bucket := aws.String(d.bucket)

		_, err := d.s3client.HeadObject(egCtx, &s3.HeadObjectInput{
			Key:    key,
			Bucket: bucket,
		})
		if err == nil {
			// File is already uploaded.
			bar.Add(int(doltSize))
			return nil
		}

		rd := NewProgressBarReader(doltF, bar)
		_, err = uploader.Upload(egCtx, &s3.PutObjectInput{
			Key:           key,
			Bucket:        bucket,
			Body:          rd,
			ContentLength: aws.Int64(doltSize),
		})
		return err
	})
	eg.Go(func() error {
		key := aws.String(filepath.Base(strings.TrimSuffix(artifacts.BinTarPath, ".tar")))
		bucket := aws.String(d.bucket)

		_, err := d.s3client.HeadObject(egCtx, &s3.HeadObjectInput{
			Key:    key,
			Bucket: bucket,
		})
		if err == nil {
			// File is already uploaded.
			bar.Add(int(binSize))
			return nil
		}

		rd := NewProgressBarReader(binF, bar)
		_, err = uploader.Upload(egCtx, &s3.PutObjectInput{
			Key:           key,
			Bucket:        bucket,
			Body:          rd,
			ContentLength: aws.Int64(binSize),
		})
		return err
	})
	eg.Go(func() error {
		key := aws.String(filepath.Base(strings.TrimSuffix(artifacts.TestsTarPath, ".tar")))
		bucket := aws.String(d.bucket)

		_, err := d.s3client.HeadObject(egCtx, &s3.HeadObjectInput{
			Key:    key,
			Bucket: bucket,
		})
		if err == nil {
			// File is already uploaded.
			bar.Add(int(testsSize))
			return nil
		}

		rd := NewProgressBarReader(testsF, bar)
		_, err = uploader.Upload(egCtx, &s3.PutObjectInput{
			Key:           key,
			Bucket:        bucket,
			Body:          rd,
			ContentLength: aws.Int64(testsSize),
		})
		return err
	})

	return eg.Wait()
}
