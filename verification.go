package sfv

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
)

type Entry struct {
	Directory string
	Name      string
	Value     uint32
}

type File struct {
	FullName string
	Entries  []Entry
}

type FileOut struct {
	File
	Done chan bool
}

type SyntaxError struct {
	Line     int
	template string
	inner    error
}

type SFVer struct {
	parallelism int
}

func (entry Entry) String() string {
	buf := [4]byte{}
	binary.LittleEndian.PutUint32(buf[:], entry.Value)
	return entry.Name + " " + hex.EncodeToString(buf[:])
}

func (e SyntaxError) Error() string {
	return fmt.Sprintf(e.template, e.Line, e.inner)
}

func (sfver SFVer) Read(fullName string) (*File, error) {
	f, err := os.Open(fullName)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" || line[0] == 59 { // ignore semicolons
			continue
		}
		lines = append(lines, line)
	}

	temp := scanner.Err()
	if temp != nil && temp != io.EOF {
		return nil, temp
	}

	out := &File{}
	out.FullName = fullName

	directory := path.Dir(fullName)

	for idx, line := range lines {
		sep := strings.LastIndex(line, " ")
		if sep == -1 {
			return nil, SyntaxError{Line: idx + 1, template: "Invalid sfv entry at line %d (%v).", inner: err}
		}

		name := strings.TrimSpace(line[0 : sep-1])
		value := strings.TrimSpace(line[sep:len(line)])

		hash, err := hex.DecodeString(value)
		if err != nil {
			return nil, SyntaxError{Line: idx + 1, template: "Invalid hash sum at line %d (%v).", inner: err}
		}

		ordinal := binary.LittleEndian.Uint32(hash)

		entry := Entry{Directory: directory, Name: name, Value: ordinal}
		out.Entries = append(out.Entries, entry)
	}

	return out, nil
}

func (sfver SFVer) Create(files <-chan string, cancel <-chan bool) (*FileOut, error) {
	output := make(chan Entry, sfver.parallelism)
	var wg sync.WaitGroup
	for i := 0; i < sfver.parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case _, ok := <-files:
					if !ok {
						break
					}
				case <-cancel:
					break
				}
			}
		}()
	}

	var result FileOut
	go func() {
		entries := make([]Entry, 10)
		for {
			entry, ok := <-output
			if !ok {
				break
			}
			entries = append(entries, entry)
		}
	}()

	go func() {
		wg.Wait()
		result.Done <- true
	}()
	return &result, nil
}

func New(parallelism int) (result SFVer) {
	if parallelism < 1 {
		parallelism = 1
	} else if max := maxParallelism(); parallelism > maxParallelism() {
		parallelism = max
	}
	return SFVer{parallelism: parallelism}
}

// http://stackoverflow.com/questions/13234749/golang-how-to-verify-number-of-processors-on-which-a-go-program-is-running
func maxParallelism() int {
	maxProcs := runtime.GOMAXPROCS(0)
	numCPU := runtime.NumCPU()
	if maxProcs < numCPU {
		return maxProcs
	}
	return numCPU
}
