package main

import (
	"fmt"
	"log"
	"os"
	"runtime"

	"github.com/cwlbraa/sfv"
)

func main() {
	verifier := sfv.New(runtime.NumCPU())
	if len(os.Args) < 2 {
		fmt.Println("please provide an sfv file to verify")
	}
	filename := os.Args[1]
	cancel := make(chan interface{})
	result, err := verifier.Verify(filename, cancel)
	if err != nil {
		log.Fatal(err)
	}

	for {
		status, ok := <-result.Processed
		if !ok {
			fmt.Println("done")
			break
		}

		fmt.Printf("got:\n%#v\n, but expected\n%#v\n", status.Entry, status.Expected)
		// if status.Status != sfv.Good {
		// 	fmt.Printf("got:\n%#v\n, but expected\n%#v\n", status.Entry, status.Expected)
		// }
	}
}
