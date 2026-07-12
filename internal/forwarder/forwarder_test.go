package forwarder

import (
	"errors"
	"testing"
)

type fakeController struct {
	enabled        bool
	granted        map[string]bool
	onWhitelist    bool
	enableCalls    int
	grantCalls     map[string]int
	whitelistCalls int
	enableErr      error
	grantErr       error
	whitelistErr   error
}

func newFakeController() *fakeController {
	return &fakeController{granted: map[string]bool{}, grantCalls: map[string]int{}}
}

func (f *fakeController) IsEnabled() (bool, error) { return f.enabled, nil }
func (f *fakeController) Enable() error {
	f.enableCalls++
	if f.enableErr != nil {
		return f.enableErr
	}
	f.enabled = true
	return nil
}
func (f *fakeController) IsPermissionGranted(p string) (bool, error) { return f.granted[p], nil }
func (f *fakeController) GrantPermission(p string) error {
	f.grantCalls[p]++
	if f.grantErr != nil {
		return f.grantErr
	}
	f.granted[p] = true
	return nil
}
func (f *fakeController) IsOnDozeWhitelist() (bool, error) { return f.onWhitelist, nil }
func (f *fakeController) AddToDozeWhitelist() error {
	f.whitelistCalls++
	if f.whitelistErr != nil {
		return f.whitelistErr
	}
	f.onWhitelist = true
	return nil
}

var testPerms = []string{"RECEIVE_SMS", "READ_SMS", "SEND_SMS"}

func TestEnsureHealthy_AllFine_NoActions(t *testing.T) {
	f := newFakeController()
	f.enabled = true
	f.onWhitelist = true
	for _, p := range testPerms {
		f.granted[p] = true
	}

	fixed, err := EnsureHealthy(f, testPerms)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed) != 0 {
		t.Fatalf("expected no actions, got %v", fixed)
	}
	if f.enableCalls != 0 || f.whitelistCalls != 0 {
		t.Fatal("expected no corrective calls when everything is already healthy")
	}
}

func TestEnsureHealthy_DisabledApp_ReEnabled(t *testing.T) {
	f := newFakeController()
	f.onWhitelist = true
	for _, p := range testPerms {
		f.granted[p] = true
	}

	fixed, err := EnsureHealthy(f, testPerms)
	if err != nil {
		t.Fatal(err)
	}
	if f.enableCalls != 1 {
		t.Fatalf("expected Enable() called once, got %d", f.enableCalls)
	}
	if len(fixed) != 1 {
		t.Fatalf("expected 1 fix logged, got %v", fixed)
	}
}

func TestEnsureHealthy_RevokedPermissions_ReGranted(t *testing.T) {
	f := newFakeController()
	f.enabled = true
	f.onWhitelist = true
	// None of the permissions are granted - simulates the exact V1
	// scenario (all three revoked together).

	fixed, err := EnsureHealthy(f, testPerms)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed) != len(testPerms) {
		t.Fatalf("expected %d fixes, got %d: %v", len(testPerms), len(fixed), fixed)
	}
	for _, p := range testPerms {
		if f.grantCalls[p] != 1 {
			t.Fatalf("expected GrantPermission(%s) called once, got %d", p, f.grantCalls[p])
		}
	}
}

func TestEnsureHealthy_DroppedFromWhitelist_ReAdded(t *testing.T) {
	f := newFakeController()
	f.enabled = true
	for _, p := range testPerms {
		f.granted[p] = true
	}
	// onWhitelist left false.

	fixed, err := EnsureHealthy(f, testPerms)
	if err != nil {
		t.Fatal(err)
	}
	if f.whitelistCalls != 1 {
		t.Fatalf("expected AddToDozeWhitelist() called once, got %d", f.whitelistCalls)
	}
	if len(fixed) != 1 {
		t.Fatalf("expected 1 fix, got %v", fixed)
	}
}

func TestEnsureHealthy_GrantError_Propagates(t *testing.T) {
	f := newFakeController()
	f.enabled = true
	f.onWhitelist = true
	f.grantErr = errors.New("pm grant failed")

	_, err := EnsureHealthy(f, testPerms)
	if err == nil {
		t.Fatal("expected error to propagate from a failed grant")
	}
}
