package sfv

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
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

type FileIn struct {
	FullName string
	Entries  []Entry
}

type FileOut struct {
	FullName string
	Entries  <-chan Entry
}

type SyntaxError struct {
	Line     int
	template string
	inner    error
}

type EntryStatus struct {
	Entry  *Entry
	Status int
}

type Verification struct {
	FileIn
	Processed <-chan *EntryStatus
}

type SFVer struct {
	parallelism int
}

var CANCELED = errors.New("Calculation canceled.")

const (
	NotChecked = 0
	Checking   = 1
	Good       = 2
	Missing    = 3
	Bad        = 4
	Error      = 5
)

func (entry Entry) String() string {
	buf := [4]byte{}
	binary.BigEndian.PutUint32(buf[:], entry.Value)
	return entry.Name + " " + strings.ToUpper(hex.EncodeToString(buf[:]))
}

func (entry Entry) FullName() string {
	return path.Join(entry.Directory, entry.Name)
}

func (e SyntaxError) Error() string {
	return fmt.Sprintf(e.template, e.Line, e.inner)
}

// Create hash incoming files and return a publishing channel.
func (sfver SFVer) Create(files <-chan string, cancel <-chan interface{}) (*FileOut, error) {
	output := make(chan Entry, 256)

	var result FileOut
	result.Entries = output

	var wg sync.WaitGroup
	wg.Add(sfver.parallelism)

	for i := 0; i < sfver.parallelism; i++ {
		go func() {
			defer func() {
				wg.Done()
			}()
			for {
				select {
				case fullName, ok := <-files:
					if !ok {
						return
					}
					hash, err := Calculate(fullName, cancel)
					if err != nil {
						if err == CANCELED {
							return
						} else {
							panic(err)
						}
					}
					entry := Entry{Directory: path.Dir(fullName), Name: path.Base(fullName), Value: hash}
					output <- entry
				case <-cancel:
					return
				}
			}
		}()
	}

	go func() {
		wg.Wait()
		close(output)
	}()

	return &result, nil
}

func (sfver SFVer) Verify(fullName string, cancel <-chan interface{}) (*Verification, error) {
	file, err := Read(fullName)
	if err != nil {
		return nil, err
	}

	var mutex sync.Mutex
	hashMap := make(map[string]Entry)
	files := make(chan string, sfver.parallelism)
	processed := make(chan *EntryStatus, len(file.Entries))
	go func() {
		for _, e := range file.Entries {
			mutex.Lock()
			hashMap[e.Name] = e
			mutex.Unlock()
			_, err := os.Lstat(e.FullName())
			if err != nil {
				var entryStatus EntryStatus
				entryStatus.Entry = &e
				if os.IsNotExist(err) {
					entryStatus.Status = Missing
				} else if os.IsPermission(err) {
					entryStatus.Status = Error
				}
				processed <- &entryStatus
			} else {
				files <- e.FullName()
			}
		}
		close(files)
	}()

	out, err := sfver.Create(files, cancel)
	if err != nil {
		return nil, err
	}

	var result Verification
	result.Entries = file.Entries
	result.Processed = processed

	go func() {
		for {
			entry, ok := <-out.Entries
			if !ok {
				close(processed)
				return
			}
			mutex.Lock()
			compare := hashMap[entry.Name]
			mutex.Unlock()

			var entryStatus EntryStatus
			entryStatus.Entry = &entry

			if compare.Value == entry.Value {
				entryStatus.Status = Good
			} else {
				entryStatus.Status = Bad
			}

			processed <- &entryStatus
		}
	}()

	return &result, nil
}

// New creates a new SFVer structure.
func New(parallelism int) (result SFVer) {
	if parallelism < 1 {
		parallelism = 1
	} else if max := maxParallelism(); parallelism > maxParallelism() {
		parallelism = max
	}
	return SFVer{parallelism: parallelism}
}

// Calculate calculates the checksum for an given file.
func Calculate(fullName string, cancel <-chan interface{}) (uint32, error) {
	f, err := os.Open(fullName)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	buffer := make([]byte, 8192)
	hasher := crc32.NewIEEE()
ForLoop:
	for {
		select {
		case <-cancel:
			return 0, CANCELED
		default:
			read, err := f.Read(buffer)
			if err != nil && err != io.EOF {
				return 0, err
			}
			if read == 0 {
				break ForLoop
			}

			data := buffer[0:read]

			hasher.Write(data)
		}
	}

	return hasher.Sum32(), nil
}

// Read reads a SFV file into a FileIn structure ready for verifying.
func Read(fullName string) (*FileIn, error) {
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
		if line == "" || line[0] == 59 { // ignore semicolons as comments
			continue
		}
		lines = append(lines, line)
	}

	temp := scanner.Err()
	if temp != nil && temp != io.EOF {
		return nil, temp
	}

	out := &FileIn{}
	out.FullName = fullName

	directory := path.Dir(fullName)

	for idx, line := range lines {
		sep := strings.LastIndex(line, " ")
		if sep == -1 {
			return nil, SyntaxError{Line: idx + 1, template: "Invalid sfv entry at line %d (%v).", inner: err}
		}

		name := strings.TrimSpace(line[0:sep])
		value := strings.TrimSpace(line[sep+1 : len(line)])

		hash, err := hex.DecodeString(value)
		if err != nil {
			return nil, SyntaxError{Line: idx + 1, template: "Invalid hash sum at line %d (%v).", inner: err}
		}

		ordinal := binary.BigEndian.Uint32(hash)

		entry := Entry{Directory: directory, Name: name, Value: ordinal}
		out.Entries = append(out.Entries, entry)
	}

	return out, nil
}

func Write(fullName string, entries []Entry) error {
	f, err := os.Create(fullName)
	if err != nil {
		return err
	}

	defer f.Close()
	for _, e := range entries {
		_, err := f.WriteString(e.String())
		if err != nil {
			return err
		}
	}

	return nil
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
