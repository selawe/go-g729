// benchcheck parses the output of "go test -bench" and fails if any benchmark
// exceeds a user-defined ns/op threshold. It is intended as a lightweight CI
// performance gate that requires no external dependencies.
//
// Usage:
//
//	go test -bench=. -benchtime=5s ./... > bench.txt
//	go run ./cmd/benchcheck \
//	    --file=bench.txt \
//	    --gate="BenchmarkEncodeG729A=50000" \
//	    --gate="BenchmarkDecodeSpeech=15000" \
//	    --gate="BenchmarkSearchAlgebraicA=25000"
//
// Exit codes:
//
//	0  All gates passed (or no gates were specified).
//	1  One or more benchmarks exceeded their threshold.
//	2  Usage error or file read failure.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// gate holds one parsed --gate flag value.
type gate struct {
	name      string
	maxNsPerOp int64
}

// multiFlag is a flag.Value that accumulates repeated --gate values.
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ", ") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func main() {
	var gateFlags multiFlag
	file := flag.String("file", "", "path to 'go test -bench' output file (default: stdin)")
	flag.Var(&gateFlags, "gate", "NAME=MAX_NS_PER_OP threshold; may be repeated")
	flag.Parse()

	if len(gateFlags) == 0 {
		fmt.Fprintln(os.Stderr, "benchcheck: no --gate flags specified; nothing to check")
		os.Exit(0)
	}

	// Parse gate thresholds
	gates := make([]gate, 0, len(gateFlags))
	for _, g := range gateFlags {
		parts := strings.SplitN(g, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "benchcheck: invalid gate %q (want NAME=NS)\n", g)
			os.Exit(2)
		}
		ns, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || ns <= 0 {
			fmt.Fprintf(os.Stderr, "benchcheck: invalid threshold in %q: %v\n", g, err)
			os.Exit(2)
		}
		gates = append(gates, gate{name: parts[0], maxNsPerOp: ns})
	}

	// Open input
	var scanner *bufio.Scanner
	if *file == "" {
		scanner = bufio.NewScanner(os.Stdin)
	} else {
		f, err := os.Open(*file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchcheck: cannot open %q: %v\n", *file, err)
			os.Exit(2)
		}
		defer f.Close()
		scanner = bufio.NewScanner(f)
	}

	// Parse benchmark lines
	// Format: BenchmarkFoo-N   12345   67890 ns/op   0 B/op   0 allocs/op
	results := make(map[string]int64)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "Benchmark") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// Strip goroutine suffix: "BenchmarkFoo-16" → "BenchmarkFoo"
		name := fields[0]
		if idx := strings.LastIndex(name, "-"); idx != -1 {
			name = name[:idx]
		}

		// Find "ns/op" field
		for i := 1; i < len(fields)-1; i++ {
			if fields[i+1] == "ns/op" {
				ns, err := strconv.ParseFloat(fields[i], 64)
				if err == nil {
					// Keep the worst (highest) result if name appears multiple times
					if int64(ns) > results[name] {
						results[name] = int64(ns)
					}
				}
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "benchcheck: read error: %v\n", err)
		os.Exit(2)
	}

	// Check gates
	failed := 0
	passed := 0
	missing := 0
	for _, g := range gates {
		ns, found := results[g.name]
		if !found {
			fmt.Printf("  MISSING  %-40s (not found in benchmark output)\n", g.name)
			missing++
			continue
		}
		if ns > g.maxNsPerOp {
			fmt.Printf("  FAIL     %-40s %7d ns/op  >  %d ns/op (%.1f× over budget)\n",
				g.name, ns, g.maxNsPerOp, float64(ns)/float64(g.maxNsPerOp))
			failed++
		} else {
			fmt.Printf("  OK       %-40s %7d ns/op  <= %d ns/op\n",
				g.name, ns, g.maxNsPerOp)
			passed++
		}
	}

	fmt.Printf("\nbenchcheck: %d passed, %d failed, %d not found\n", passed, failed, missing)
	if failed > 0 {
		os.Exit(1)
	}
}
