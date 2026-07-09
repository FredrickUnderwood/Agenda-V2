package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/domain"
	"github.com/FredrickUnderwood/agenda-v2/internal/service"
)

type fakeMachineLister struct {
	mu    sync.Mutex
	views []service.MachineView
}

func (f *fakeMachineLister) set(views []service.MachineView) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.views = views
}

func (f *fakeMachineLister) ListViews(context.Context) ([]service.MachineView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.views, nil
}

func agentMachine(id int64, name string, online bool) service.MachineView {
	hb := time.Now()
	return service.MachineView{
		Machine: &domain.Machine{ID: id, Name: name, Host: "h", Mode: domain.MachineModeAgent, AgentLastHeartbeatAt: &hb},
		Online:  online,
	}
}

// newMachineTestMonitor wires a real AlertService against a local webhook so
// SendToAll is observable, mirroring alert_rule_monitor_test's harness.
func newMachineTestMonitor(t *testing.T) (*MachineMonitor, *fakeMachineLister, chan capturedWebhook) {
	t.Helper()
	captured := make(chan capturedWebhook, 10)
	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg capturedWebhook
		_ = json.NewDecoder(r.Body).Decode(&msg)
		captured <- msg
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(webhookSrv.Close)

	settings := fakeSettingsReader{prefix: map[string]string{
		"alert.channel.custom.test": `{"webhook_url":"` + webhookSrv.URL + `","enabled":true}`,
	}}
	alertSvc := service.NewAlertService(settings, nil)

	lister := &fakeMachineLister{}
	mon := NewMachineMonitor(lister, alertSvc, time.Second)
	return mon, lister, captured
}

func expectNoWebhook(t *testing.T, captured chan capturedWebhook) {
	t.Helper()
	select {
	case msg := <-captured:
		t.Fatalf("expected no alert, got %q", msg.Title)
	case <-time.After(150 * time.Millisecond):
	}
}

func expectWebhook(t *testing.T, captured chan capturedWebhook) capturedWebhook {
	t.Helper()
	select {
	case msg := <-captured:
		return msg
	case <-time.After(time.Second):
		t.Fatal("expected an alert, got none")
		return capturedWebhook{}
	}
}

func TestMachineMonitor_FirstObservationDoesNotAlert(t *testing.T) {
	mon, lister, captured := newMachineTestMonitor(t)
	// Startup: an already-offline agent machine must not alert on first tick.
	lister.set([]service.MachineView{agentMachine(1, "node-a", false)})
	mon.evaluateOnce()
	expectNoWebhook(t, captured)
}

func TestMachineMonitor_OnlineToOfflineAlertsThenRecovers(t *testing.T) {
	mon, lister, captured := newMachineTestMonitor(t)

	// Seed online (no alert on first observation).
	lister.set([]service.MachineView{agentMachine(1, "node-a", true)})
	mon.evaluateOnce()
	expectNoWebhook(t, captured)

	// online -> offline: a critical offline alert.
	lister.set([]service.MachineView{agentMachine(1, "node-a", false)})
	mon.evaluateOnce()
	msg := expectWebhook(t, captured)
	if msg.Level != "critical" {
		t.Fatalf("expected critical level, got %q", msg.Level)
	}
	if want := "Machine offline: node-a"; msg.Title != want {
		t.Fatalf("expected title %q, got %q", want, msg.Title)
	}

	// Staying offline must not re-alert every tick.
	mon.evaluateOnce()
	expectNoWebhook(t, captured)

	// offline -> online: an info recovery notice.
	lister.set([]service.MachineView{agentMachine(1, "node-a", true)})
	mon.evaluateOnce()
	rec := expectWebhook(t, captured)
	if rec.Level != "info" {
		t.Fatalf("expected info level, got %q", rec.Level)
	}
	if want := "Machine recovered: node-a"; rec.Title != want {
		t.Fatalf("expected title %q, got %q", want, rec.Title)
	}
}

func TestMachineMonitor_IgnoresSSHMachines(t *testing.T) {
	mon, lister, captured := newMachineTestMonitor(t)
	// An SSH machine always reports offline; it must never drive an alert.
	ssh := service.MachineView{Machine: &domain.Machine{ID: 2, Name: "ssh-box", Mode: domain.MachineModeSSH}, Online: false}
	lister.set([]service.MachineView{ssh})
	mon.evaluateOnce()
	mon.evaluateOnce()
	expectNoWebhook(t, captured)
}

func TestMachineMonitor_DeletedMachineStatePruned(t *testing.T) {
	mon, lister, _ := newMachineTestMonitor(t)
	lister.set([]service.MachineView{agentMachine(1, "node-a", true)})
	mon.evaluateOnce()
	if _, ok := mon.lastOnline[1]; !ok {
		t.Fatal("expected machine 1 to be tracked")
	}
	// Machine removed from the fleet: its remembered state is dropped.
	lister.set(nil)
	mon.evaluateOnce()
	if _, ok := mon.lastOnline[1]; ok {
		t.Fatal("expected machine 1 state to be pruned after deletion")
	}
}
