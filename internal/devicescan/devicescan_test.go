package devicescan

import (
	"errors"
	"testing"
	"time"
)

type fakeController struct {
	clients []Client
	err     error
}

func (f fakeController) ListLiveClients() ([]Client, error) { return f.clients, f.err }

type fakeStore struct {
	upserted  []string
	pruneN    int
	pruneErr  error
	upsertErr error
}

func (f *fakeStore) UpsertDeviceSeen(mac, band string, bytesDelta int64) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.upserted = append(f.upserted, mac)
	return nil
}

func (f *fakeStore) PruneStaleDevices(olderThan time.Duration) (int, error) {
	return f.pruneN, f.pruneErr
}

func TestTick_RecordsEachLiveClient(t *testing.T) {
	ctrl := fakeController{clients: []Client{{MAC: "aa:aa:aa:aa:aa:aa", Band: "wlan2"}, {MAC: "bb:bb:bb:bb:bb:bb", Band: "ncm0"}}}
	st := &fakeStore{pruneN: 2}

	seen, pruned, err := Tick(ctrl, st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != 2 {
		t.Fatalf("expected 2 clients recorded, got %d", seen)
	}
	if pruned != 2 {
		t.Fatalf("expected prune count passed through, got %d", pruned)
	}
	if len(st.upserted) != 2 {
		t.Fatalf("expected UpsertDeviceSeen called twice, got %v", st.upserted)
	}
}

func TestTick_ScanErrorPropagates(t *testing.T) {
	ctrl := fakeController{err: errors.New("ip neigh failed")}
	st := &fakeStore{}
	_, _, err := Tick(ctrl, st)
	if err == nil {
		t.Fatal("expected the scan error to propagate")
	}
}

func TestTick_NoClients_StillPrunes(t *testing.T) {
	ctrl := fakeController{}
	st := &fakeStore{pruneN: 3}
	seen, pruned, err := Tick(ctrl, st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seen != 0 || pruned != 3 {
		t.Fatalf("expected 0 seen, 3 pruned, got seen=%d pruned=%d", seen, pruned)
	}
}
