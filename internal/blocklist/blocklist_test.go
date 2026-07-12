package blocklist

import (
	"reflect"
	"testing"
)

func TestBlockArgs_InsertsDropRule(t *testing.T) {
	got := BlockArgs("aa:bb:cc:dd:ee:ff")
	want := []string{"-I", "FORWARD", "1", "-m", "mac", "--mac-source", "aa:bb:cc:dd:ee:ff", "-j", "DROP"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestUnblockArgs_DeletesDropRule(t *testing.T) {
	got := UnblockArgs("aa:bb:cc:dd:ee:ff")
	want := []string{"-D", "FORWARD", "-m", "mac", "--mac-source", "aa:bb:cc:dd:ee:ff", "-j", "DROP"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
