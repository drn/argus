package hera

import (
	"errors"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/testutil"
)

// --- fakes ---

// fakeStore records calls and returns canned results.
type fakeStore struct {
	messages   []*db.HeraMessage
	nextID     int64
	roles      map[int64]*db.HeraRole
	bindings   map[int64]*db.HeraBinding // keyed by roleID
	readCalls  []readCall
	stampCalls []stampCall
	sendErr    error
	bindingErr error // overrides bindings lookup result
}

type readCall struct {
	roleID int64
	ids    []int64
}

type stampCall struct {
	id   int64
	mode string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		roles:    make(map[int64]*db.HeraRole),
		bindings: make(map[int64]*db.HeraBinding),
	}
}

func (f *fakeStore) addRole(id int64, name string) {
	f.roles[id] = &db.HeraRole{ID: id, Name: name}
}

func (f *fakeStore) addBinding(roleID int64, taskID string) {
	f.bindings[roleID] = &db.HeraBinding{RoleID: roleID, ArgusTaskID: taskID}
}

func (f *fakeStore) SendHeraMessage(fromRoleID, toRoleID int64, body, tldr string, inReplyTo *int64) (*db.HeraMessage, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.nextID++
	m := &db.HeraMessage{
		ID:           f.nextID,
		FromRoleID:   fromRoleID,
		ToRoleID:     toRoleID,
		Body:         body,
		Tldr:         tldr,
		InReplyTo:    inReplyTo,
		DeliveryMode: db.HeraDeliveryPending,
	}
	f.messages = append(f.messages, m)
	return m, nil
}

func (f *fakeStore) HeraInbox(roleID int64) ([]*db.HeraMessage, error) {
	var out []*db.HeraMessage
	for _, m := range f.messages {
		if m.ToRoleID == roleID && m.ReadAt == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeStore) MarkHeraMessagesRead(roleID int64, ids []int64) (int, error) {
	f.readCalls = append(f.readCalls, readCall{roleID: roleID, ids: ids})
	return len(ids), nil
}

func (f *fakeStore) HeraMessagesByIDs(ids []int64) ([]*db.HeraMessage, error) {
	set := make(map[int64]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	var out []*db.HeraMessage
	for _, m := range f.messages {
		if set[m.ID] {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeStore) MarkHeraMessageDelivered(id int64, mode string) error {
	f.stampCalls = append(f.stampCalls, stampCall{id: id, mode: mode})
	return nil
}

func (f *fakeStore) HeraLiveBindingByRole(roleID int64) (*db.HeraBinding, error) {
	if f.bindingErr != nil {
		return nil, f.bindingErr
	}
	b, ok := f.bindings[roleID]
	if !ok {
		return nil, db.ErrHeraNotFound
	}
	return b, nil
}

func (f *fakeStore) HeraRole(id int64) (*db.HeraRole, error) {
	r, ok := f.roles[id]
	if !ok {
		return nil, db.ErrHeraNotFound
	}
	return r, nil
}

// fakeNotifier records ReliableNotify and Cancel calls.
type fakeNotifier struct {
	notifyCalls []notifyCall
	cancelCalls []cancelCall
}

type notifyCall struct {
	taskID     string
	text       string
	deliveryID string
}

type cancelCall struct {
	taskID     string
	deliveryID string
}

func (f *fakeNotifier) ReliableNotify(taskID, text, deliveryID string, _ notify.NotifyOpts) func() {
	f.notifyCalls = append(f.notifyCalls, notifyCall{taskID: taskID, text: text, deliveryID: deliveryID})
	return func() {}
}

func (f *fakeNotifier) Cancel(taskID, deliveryID string) {
	f.cancelCalls = append(f.cancelCalls, cancelCall{taskID: taskID, deliveryID: deliveryID})
}

// --- tests ---

func TestServiceSend_HappyPath(t *testing.T) {
	store := newFakeStore()
	store.addRole(1, "coord")
	store.addRole(2, "worker")
	store.addBinding(2, "task-abc") // recipient has a live binding

	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	msg, err := svc.Send(1, 2, "hello world", "hello summary", nil)
	testutil.NoError(t, err)
	testutil.Equal(t, msg.FromRoleID, int64(1))
	testutil.Equal(t, msg.ToRoleID, int64(2))

	// Notifier called once.
	testutil.Equal(t, len(nudger.notifyCalls), 1)
	nc := nudger.notifyCalls[0]
	testutil.Equal(t, nc.taskID, "task-abc")
	testutil.Equal(t, nc.deliveryID, fmt.Sprintf("hera:%d", msg.ID))
	testutil.Contains(t, nc.text, "coord")
	testutil.Contains(t, nc.text, "hello summary")

	// Stamp called with idle_submit.
	testutil.Equal(t, len(store.stampCalls), 1)
	testutil.Equal(t, store.stampCalls[0].id, msg.ID)
	testutil.Equal(t, store.stampCalls[0].mode, db.HeraDeliveryIdleSubmit)
}

func TestServiceSend_NoLiveBinding(t *testing.T) {
	store := newFakeStore()
	store.addRole(1, "coord")
	store.addRole(2, "worker")
	// No binding for role 2.

	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	msg, err := svc.Send(1, 2, "body", "tldr", nil)
	testutil.NoError(t, err) // soft-fail — no error
	testutil.Equal(t, msg.ID, int64(1))

	// Notifier NOT called.
	testutil.Equal(t, len(nudger.notifyCalls), 0)

	// Stamp called with queued_no_binding.
	testutil.Equal(t, len(store.stampCalls), 1)
	testutil.Equal(t, store.stampCalls[0].mode, db.HeraDeliveryQueuedNoBinding)
}

func TestServiceSend_NilNotifier(t *testing.T) {
	store := newFakeStore()
	store.addRole(1, "coord")
	store.addRole(2, "worker")
	store.addBinding(2, "task-abc")

	svc := New(store, nil) // nil notifier

	msg, err := svc.Send(1, 2, "body", "tldr", nil)
	testutil.NoError(t, err)
	testutil.Equal(t, msg.ID, int64(1))

	// No stamps — nil notifier path returns before any delivery logic.
	testutil.Equal(t, len(store.stampCalls), 0)
}

func TestServiceSend_StoreError(t *testing.T) {
	store := newFakeStore()
	store.sendErr = db.ErrHeraMessageSelfSend

	svc := New(store, &fakeNotifier{})

	_, err := svc.Send(1, 1, "body", "tldr", nil)
	testutil.ErrorIs(t, err, db.ErrHeraMessageSelfSend)
}

func TestServiceSend_NotifierFailDoesNotFailSend(t *testing.T) {
	// Simulate a binding lookup error (not ErrHeraNotFound) to exercise the
	// soft-fail path for unexpected errors.
	store := newFakeStore()
	store.addRole(1, "coord")
	store.addRole(2, "worker")
	unexpectedErr := errors.New("db flap")
	store.bindingErr = unexpectedErr

	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	msg, err := svc.Send(1, 2, "body", "tldr", nil)
	testutil.NoError(t, err) // soft-fail
	testutil.Equal(t, msg.ID, int64(1))

	// No notifier call on unexpected binding error.
	testutil.Equal(t, len(nudger.notifyCalls), 0)
}

func TestServiceSend_DoorbellLineFormat(t *testing.T) {
	store := newFakeStore()
	store.addRole(10, "my-coordinator")
	store.addRole(20, "worker-bee")
	store.addBinding(20, "task-xyz")

	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	_, err := svc.Send(10, 20, "big body here", "short summary", nil)
	testutil.NoError(t, err)

	testutil.Equal(t, len(nudger.notifyCalls), 1)
	line := nudger.notifyCalls[0].text
	testutil.Contains(t, line, "[hera from my-coordinator]")
	testutil.Contains(t, line, "short summary")
	testutil.Contains(t, line, "msg #1")
}

func TestServiceSend_FallbackRoleNameOnLookupFailure(t *testing.T) {
	store := newFakeStore()
	// Do NOT add role 10 → HeraRole lookup will fail.
	store.addRole(20, "worker")
	store.addBinding(20, "task-abc")

	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	_, err := svc.Send(10, 20, "body", "tldr", nil)
	testutil.NoError(t, err)

	// Doorbell still fires with fallback label.
	testutil.Equal(t, len(nudger.notifyCalls), 1)
	testutil.Contains(t, nudger.notifyCalls[0].text, "role:10")
}

func TestServiceInbox(t *testing.T) {
	store := newFakeStore()
	store.addRole(1, "coord")
	store.addRole(2, "worker")
	store.addBinding(2, "task-abc")

	// Pre-populate a message in the inbox.
	store.messages = append(store.messages, &db.HeraMessage{
		ID:         42,
		FromRoleID: 1,
		ToRoleID:   2,
	})

	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	msgs, err := svc.Inbox(2)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)

	// Cancel called for the message.
	testutil.Equal(t, len(nudger.cancelCalls), 1)
	testutil.Equal(t, nudger.cancelCalls[0].taskID, "task-abc")
	testutil.Equal(t, nudger.cancelCalls[0].deliveryID, "hera:42")
}

func TestServiceInbox_NoCancelWithoutBinding(t *testing.T) {
	store := newFakeStore()
	store.addRole(1, "coord")
	store.addRole(2, "worker")
	// No binding for role 2.

	store.messages = append(store.messages, &db.HeraMessage{
		ID:         42,
		FromRoleID: 1,
		ToRoleID:   2,
	})

	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	msgs, err := svc.Inbox(2)
	testutil.NoError(t, err)
	testutil.Equal(t, len(msgs), 1)

	// No cancel without a binding.
	testutil.Equal(t, len(nudger.cancelCalls), 0)
}

func TestServiceMarkRead(t *testing.T) {
	store := newFakeStore()
	store.addRole(2, "worker")
	store.addBinding(2, "task-abc")

	store.messages = append(store.messages,
		&db.HeraMessage{ID: 10, ToRoleID: 2},
		&db.HeraMessage{ID: 11, ToRoleID: 2},
	)

	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	n, err := svc.MarkRead(2, []int64{10, 11})
	testutil.NoError(t, err)
	testutil.Equal(t, n, 2)

	// MarkRead recorded.
	testutil.Equal(t, len(store.readCalls), 1)
	testutil.Equal(t, store.readCalls[0].roleID, int64(2))

	// Cancel called for each message.
	testutil.Equal(t, len(nudger.cancelCalls), 2)
	ids := map[string]bool{
		nudger.cancelCalls[0].deliveryID: true,
		nudger.cancelCalls[1].deliveryID: true,
	}
	testutil.Equal(t, ids["hera:10"], true)
	testutil.Equal(t, ids["hera:11"], true)
}

func TestServiceGetByIDs(t *testing.T) {
	store := newFakeStore()
	store.messages = append(store.messages,
		&db.HeraMessage{ID: 5, ToRoleID: 1},
		&db.HeraMessage{ID: 7, ToRoleID: 2},
	)

	svc := New(store, nil)

	got, err := svc.GetByIDs([]int64{5, 99})
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].ID, int64(5))
}

func TestHeraDeliveryID(t *testing.T) {
	testutil.Equal(t, heraDeliveryID(1), "hera:1")
	testutil.Equal(t, heraDeliveryID(9999), "hera:9999")
}

// --- DeliverToRole (add-model-menu-selection hera_retier) ---

// TestServiceDeliverToRole_HappyPath asserts DeliverToRole resolves the role's
// live binding and reuses the SAME ReliableNotify primitive Send uses for
// message delivery — no new write path.
func TestServiceDeliverToRole_HappyPath(t *testing.T) {
	store := newFakeStore()
	store.addBinding(2, "task-abc")

	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	err := svc.DeliverToRole(2, "/model opus", "hera-retier:2:1")
	testutil.NoError(t, err)

	testutil.Equal(t, len(nudger.notifyCalls), 1)
	testutil.Equal(t, nudger.notifyCalls[0].taskID, "task-abc")
	testutil.Equal(t, nudger.notifyCalls[0].text, "/model opus")
	testutil.Equal(t, nudger.notifyCalls[0].deliveryID, "hera-retier:2:1")
}

// TestServiceDeliverToRole_NoLiveBinding asserts a role with no live binding
// (never materialized, or ended) returns ErrHeraNotFound and delivers nothing.
func TestServiceDeliverToRole_NoLiveBinding(t *testing.T) {
	store := newFakeStore()
	nudger := &fakeNotifier{}
	svc := New(store, nudger)

	err := svc.DeliverToRole(2, "/model opus", "id")
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	testutil.Equal(t, len(nudger.notifyCalls), 0)
}

// TestServiceDeliverToRole_NilNotifier asserts a nil notifier returns an
// explicit error — unlike Send's soft-fail-and-persist, retier delivery has
// nothing durable to fall back to, so silently doing nothing would be a
// silent no-op the caller can't detect.
func TestServiceDeliverToRole_NilNotifier(t *testing.T) {
	store := newFakeStore()
	store.addBinding(2, "task-abc")
	svc := New(store, nil)

	err := svc.DeliverToRole(2, "/model opus", "id")
	testutil.Error(t, err)
}
