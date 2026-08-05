package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func main() {
	inputPath := flag.String("input", "benchmark.txt", "benchmark output file, or - for stdin")
	outputPath := flag.String("output", "benchmark.html", "HTML report output file")
	title := flag.String("title", "h3 路由性能对比", "report title")
	flag.Parse()

	if err := run(*inputPath, *outputPath, *title, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "report:", err)
		os.Exit(1)
	}
	fmt.Println("generated", *outputPath)
}

func run(inputPath, outputPath, title string, generatedAt time.Time) error {
	input, err := readInput(inputPath)
	if err != nil {
		return err
	}

	report, err := parseBenchmark(input)
	if err != nil {
		return err
	}
	report.Title = title
	report.GeneratedAt = generatedAt.Format("2006-01-02 15:04:05 MST")

	var output bytes.Buffer
	if err := renderReport(&output, report); err != nil {
		return fmt.Errorf("render HTML: %w", err)
	}
	if err := os.WriteFile(outputPath, output.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	return nil
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		value, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return value, nil
	}

	value, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return value, nil
}
