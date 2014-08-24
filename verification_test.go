// verification_test.go
package sfv

import (
	"testing"
)

func TestReadFault(t *testing.T) {
	_, err := Read("data/fault.sfv")
	if _, ok := err.(SyntaxError); !ok {
		t.Fatal(err)
	}
}

func TestReadWithComment(t *testing.T) {
	result, err := Read("data/comment.sfv")
	if err != nil {
		t.Fatal(err)
	}

	if len(result.Entries) != 2 {
		t.Fatal("Reading comment line failed.")
	}
}

func TestCompleteVerify(t *testing.T) {
	cancel := make(chan interface{})
	check := New(2)
	result, err := check.Verify("data/data.sfv", cancel)
	if err != nil {
		t.Fatal(err)
	} else {
		for {
			status, ok := <-result.Processed
			if !ok {
				break
			}

			if status.Status != Good {
				t.Fatalf("%v checksum mismatch.", status.Entry)
			}
		}
	}
}
