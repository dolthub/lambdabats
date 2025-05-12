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
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"
)

// Download a supported C compiler targeting linux-arm64 so we can build a
// statically compiled dolt binary. Return environment variables for
// configuring CGO to compile with this compiler.
func StageCompiler() ([]string, error) {
	type location struct {
		url string
		sha string
	}
	var urls = map[string]location{
		"darwin-arm64": {
			url: "https://dolthub-tools.s3.us-west-2.amazonaws.com/gcc/host=aarch64-darwin/target=linux-musl/20240515_0.0.2.tar.xz",
			sha: "c3fe69b5f412c17f18efc8ddcdec4128f0103242c76b99adb3cdcf8a2c45ec89",
		},
		"darwin-amd64": { // This is probably never used. Who has macOS x86_64 anymore?
			url: "https://dolthub-tools.s3.us-west-2.amazonaws.com/gcc/host=x86_64-darwin/target=linux-musl/20240515_0.0.2.tar.xz",
			sha: "f1eda39fa81a3eaab4f79f0f010a2d6bf0aea395e65b3a6e87541f55cf2ac853",
		},
		"linux-arm64": {
			url: "https://dolthub-tools.s3.us-west-2.amazonaws.com/gcc/host=aarch64-linux/target=linux-musl/20240515_0.0.2.tar.xz",
			sha: "b603a5c636547e1cd0dc6cf1bba5a1f67aacb8dd21f1b12582786497311f1fa9",
		},
		"linux-amd64": {
			url: "https://dolthub-tools.s3.us-west-2.amazonaws.com/gcc/host=x86_64-linux/target=linux-musl/20240515_0.0.2.tar.xz",
			sha: "befaa4d83d843b8a57ea0e6a16980ffa5b5ba575f4428adec1f7f5b1aa7671f1",
		},
	}

	// Pull down the toolchain for the HOST platform, not the target platform.
	plat := runtime.GOOS + "-" + runtime.GOARCH
	loc, ok := urls[plat]
	if !ok {
		return nil, fmt.Errorf("unsupported runtime platform for lambda bats, %s; lambdabats needs to download a C toolchain targetting aarch64-linux-musl to run successfully", plat)
	}

	gnuArch := "x86_64"
	if runtime.GOARCH == "arm64" {
		gnuArch = "aarch64"
	}

	dest := filepath.Join(os.TempDir(), loc.sha)
	finalVars := []string{
		"CGO_ENABLED=1",
		fmt.Sprintf("PATH=%s/bin%c%s", dest, filepath.ListSeparator, os.Getenv("PATH")),
		fmt.Sprintf("CC=%s-linux-musl-gcc", gnuArch),
		fmt.Sprintf("AS=%s-linux-musl-as", gnuArch),
		"CGO_LDFLAGS=-static -s",
	}
	_, err := os.Stat(dest)
	if err == nil {
		// Toolchain is already downloaded and extracted.
		return finalVars, nil
	}
	f, err := os.CreateTemp("", "lambdabats-toolchain-download")
	if err != nil {
		return nil, fmt.Errorf("could not create temp file for toolchain download: %w", err)
	}
	defer os.Remove(f.Name())
	resp, err := http.Get(loc.url)
	if err != nil {
		return nil, fmt.Errorf("could not HTTP GET toolchain url: %s: %w", loc.url, err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected HTTP status for HTTP GET toolchain url: %s: %d", loc.url, resp.Status)
	}
	h := sha256.New()
	w := io.MultiWriter(f, h)
	_, err = io.Copy(w, resp.Body)
	resp.Body.Close()
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("could not copy all bytes from response body of toolchain url: %s: %w", loc.url, err)
	}
	if hashRes := hex.EncodeToString(h.Sum(nil)); hashRes != loc.sha {
		return nil, fmt.Errorf("downloading toolchain failed; download checksum (%s) did not match expected checksum (%s)", hashRes, loc.sha)
	}
	dir, err := os.MkdirTemp("", "extracted-lambdabats-toolchain-download")
	if err != nil {
		return nil, fmt.Errorf("could not create temp directory for toolchain extraction: %w", err)
	}
	defer os.RemoveAll(dir)
	out, err := exec.Command("tar", "Jx", "-C", dir, "--strip-components", "1", "-f", f.Name()).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("could not create extract downloaded toolchain: %s: %w", string(out), err)
	}
	err = os.Rename(dir, dest)
	if err != nil {
		return nil, fmt.Errorf("could not rename extracted toolchain to final destination: %w", err)
	}
	return finalVars, nil
}

func BuildTestsFile(doltSrcDir, arch string) (UploadArtifacts, error) {
	binDir := filepath.Join(os.TempDir(), uuid.New().String())
	defer os.RemoveAll(binDir)

	doltBinFilePath := filepath.Join(binDir, "dolt")
	compileEnv := append(os.Environ(), "GOOS=linux", "GOARCH="+arch)
	err := RunWithSpinner("downloading toolchain...", func() error {
		vars, err := StageCompiler()
		if err != nil {
			return fmt.Errorf("unable to stage compiler toolchain: %w", err)
		}
		compileEnv = append(compileEnv, vars...)
		return nil
	})
	if err != nil {
		return UploadArtifacts{}, err
	}
	err = RunWithSpinner("building dolt...", func() error {
		compileDolt := exec.Command("go")
		compileDolt.Args = []string{
			"go", "build", "-ldflags=-linkmode external -s -w", "-o", doltBinFilePath, "./cmd/dolt",
		}
		compileDolt.Dir = filepath.Join(doltSrcDir, "go")
		compileDolt.Env = compileEnv

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
			"go", "build", "-ldflags=-linkmode external -s -w", "-o", remotesrvBinFilePath, "./utils/remotesrv",
		}
		compileRemotesrv.Dir = filepath.Join(doltSrcDir, "go")
		compileRemotesrv.Env = compileEnv
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

func UploadTests(ctx context.Context, uploader Uploader, doltSrcDir, arch string, saveArtifacts bool) (UploadLocations, error) {
	artifacts, err := BuildTestsFile(doltSrcDir, arch)
	if err != nil {
		return UploadLocations{}, err
	}

	if saveArtifacts {
		fmt.Println(fmt.Sprintf("Dolt Binary: %s", artifacts.DoltTarPath))
		fmt.Println(fmt.Sprintf("RemoteSrv Binary: %s", artifacts.BinTarPath))
		fmt.Println(fmt.Sprintf("Test Artifacts: %s", artifacts.TestsTarPath))
	} else {
		defer os.RemoveAll(artifacts.DoltTarPath)
		defer os.RemoveAll(artifacts.BinTarPath)
		defer os.RemoveAll(artifacts.TestsTarPath)
	}
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

func NewS3Uploader(_ context.Context, cfg aws.Config, bucket string) (*S3Uploader, error) {
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
