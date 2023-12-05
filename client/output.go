package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/schollz/progressbar/v3"
	"golang.org/x/sync/errgroup"
)

type ProgressBarReader struct {
	f   *os.File
	bar *progressbar.ProgressBar
}

func NewProgressBarReader(f *os.File, bar *progressbar.ProgressBar) *ProgressBarReader {
	return &ProgressBarReader{f, bar}
}

func (r *ProgressBarReader) Read(p []byte) (n int, err error) {
	n, err = r.f.Read(p)
	r.bar.Add(n)
	return
}

func (r *ProgressBarReader) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = r.f.ReadAt(p, off)
	r.bar.Add(n)
	return
}

func (r *ProgressBarReader) Seek(offset int64, whence int) (int64, error) {
	return r.f.Seek(offset, whence)
}

func (r *ProgressBarReader) Close() error {
	r.bar.Finish()
	return r.f.Close()
}

func RunWithSpinner(message string, work func() error) error {
	done := make(chan struct{})
	var eg errgroup.Group
	eg.Go(func() error {
		defer close(done)
		return work()
	})
	eg.Go(func() error {
		i := 0
		spinner := []byte{'|', '/', '-', '\\'}
		fmt.Printf("%s %c", message, spinner[i])
		for {
			select {
			case <-done:
				fmt.Printf("\bdone\n")
				return nil
			case <-time.After(100 * time.Millisecond):
				i += 1
				if i == len(spinner) {
					i = 0
				}
				fmt.Printf("\b%c", spinner[i])
			}
		}
		return nil
	})
	return eg.Wait()
}

func OutputBatsResults(files []TestFile) int {
	blue := color.New(color.FgBlue)
	red := color.New(color.FgRed)

	numTests := 0
	numSkipped := 0
	numFailed := 0
	numFatal := 0
	for _, f := range files {
		blue.Println(f.Name)
		for _, t := range f.Tests {
			numTests += 1
			res, err := t.Runs[0].Result(t.Name)
			if err != nil {
				numFatal += 1
				red.Printf("  ✗ %s\n", t.Name)
				for _, line := range strings.Split(t.Runs[0].Response.Err, "\n") {
					red.Printf("  %s\n", line)
				}
				for _, line := range strings.Split(t.Runs[0].Response.Output, "\n") {
					red.Printf("  %s\n", line)
				}
				continue
			}
			if res.Status == TestRunResultStatus_Success {
				fmt.Printf("  ✓ %s\n", t.Name)
			} else if res.Status == TestRunResultStatus_Skipped {
				numSkipped += 1
				if res.Output == "" {
					fmt.Printf("  - %s (skipped)\n", t.Name)
				} else {
					fmt.Printf("  - %s (skipped: %s)\n", t.Name, res.Output)
				}
			} else {
				numFailed += 1
				red.Printf("  ✗ %s\n", t.Name)
				for _, line := range strings.Split(res.Output, "\n") {
					red.Printf("  %s\n", line)
				}
			}
		}
		fmt.Println()
	}
	if numFatal > 0 {
		red.Printf("%d tests, %d fatal, %d failures, %d skipped\n", numTests, numFatal, numFailed, numSkipped)
	} else if numFailed > 0 {
		red.Printf("%d tests, %d failures, %d skipped\n", numTests, numFailed, numSkipped)
	} else {
		fmt.Printf("%d tests, %d failures, %d skipped\n", numTests, numFailed, numSkipped)
	}

	if numFailed == 0 && numFatal == 0 {
		return 0
	}
	return 1
}

func OutputTAPResults(files []TestFile) int {
	numTests := 0
	numFailed := 0
	numFatal := 0
	for _, f := range files {
		numTests += len(f.Tests)
	}
	fmt.Printf("1..%d\n", numTests)
	i := 1
	for _, f := range files {
		for _, t := range f.Tests {
			res, err := t.Runs[0].Result(t.Name)
			if err != nil {
				numFatal += 1
				fmt.Printf("not ok %d %s\n", i, t.Name)
				for _, line := range strings.Split(t.Runs[0].Response.Err, "\n") {
					fmt.Printf("#%s\n", line)
				}
				for _, line := range strings.Split(t.Runs[0].Response.Output, "\n") {
					fmt.Printf("#%s\n", line)
				}
				continue
			}
			if res.Status == TestRunResultStatus_Success {
				fmt.Printf("ok %d %s\n", i, t.Name)
			} else if res.Status == TestRunResultStatus_Skipped {
				if res.Output == "" {
					fmt.Printf("ok %d %s # skip\n", i, t.Name)
				} else {
					fmt.Printf("ok %d %s # skip %s\n", i, t.Name, res.Output)
				}
			} else {
				numFailed += 1
				fmt.Printf("not ok %d %s\n", i, t.Name)
				for _, line := range strings.Split(res.Output, "\n") {
					fmt.Printf("#%s\n", line)
				}
			}
			i += 1
		}
	}

	if numFailed == 0 && numFatal == 0 {
		return 0
	}
	return 1
}
