package contract

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadBagRejectsEveryTruncation(t *testing.T) {
	v, dir := exchange(t)
	data := readTest(t, filepath.Join(dir, "bags/exec-1.jsonl"))
	for cut := 0; cut < len(data); cut++ {
		bag, err := v.ParseBag(context.Background(), data[:cut])
		if err == nil || bag.Records != nil {
			t.Fatalf("accepted prefix of %d bytes", cut)
		}
	}
	bag, err := v.ParseBag(context.Background(), data)
	if err != nil || len(bag.Records) != 2 || bag.SHA256 != hashBytes(data) {
		t.Fatalf("complete parse: %+v %v", bag, err)
	}
	bag.Records[0].Obstacles[0].PositionMM = -1
	if bag.Records[1].Obstacles[0].PositionMM != 2000 {
		t.Fatal("parsed records share storage")
	}
}

func TestPinnedHashDoesNotBypassBagValidation(t *testing.T) {
	v, dir := exchange(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	data := readTest(t, filepath.Join(dir, "bags/exec-1.jsonl"))
	data = bytes.Replace(data, []byte(`"tick":1`), []byte(`"tick":0`), 1)
	writeTest(t, filepath.Join(dir, "bags/exec-1.jsonl"), data)
	if _, err := v.ReadBag(context.Background(), root, "bags/exec-1.jsonl", hashBytes(data)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("matching hash accepted invalid structure: %v", err)
	}
}

func FuzzParseBag(f *testing.F) {
	v, err := New()
	if err != nil {
		f.Fatal(err)
	}
	data, err := os.ReadFile(examples + "/valid/bags/exec-1.jsonl")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(data)
	f.Add([]byte("\n"))
	f.Add(data[:len(data)-1])
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			t.Skip("bounded fuzz input")
		}
		bag, err := v.ParseBag(context.Background(), data)
		if err != nil {
			if bag.Records != nil {
				t.Fatal("error returned partial records")
			}
			return
		}
		if len(bag.Records) != bag.Header.RecordCount || bag.SHA256 != hashBytes(data) {
			t.Fatal("inconsistent complete bag")
		}
	})
}
