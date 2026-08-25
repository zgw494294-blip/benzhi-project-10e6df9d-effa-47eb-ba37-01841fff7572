package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"stage-rigging-clearance/internal/domain"
)

func TestCommandIdempotencyAndVersion(t *testing.T) {
	store, err := Open("file:store-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	session := domain.Session{ID: "s1", ProductionName: "测试", Venue: "剧场", ScheduledAt: time.Now().Add(time.Hour), SupervisorName: "舞监", Status: domain.StatusDraft}
	response, _ := json.Marshal(session)
	meta := CommandMeta{IdempotencyKey: "create-session", RequestDigest: "digest-a", Actor: "舞监"}
	create := func(u *Unit) (CommandResult, error) {
		if err := u.CreateSession(session); err != nil {
			return CommandResult{}, err
		}
		return CommandResult{Response: response, EventType: "session.created", SessionID: session.ID}, nil
	}
	first, replay, err := store.Command(context.Background(), meta, create)
	if err != nil || replay || string(first) != string(response) {
		t.Fatalf("首次命令异常 replay=%v err=%v", replay, err)
	}
	second, replay, err := store.Command(context.Background(), meta, create)
	if err != nil || !replay || string(second) != string(first) {
		t.Fatalf("重试应原样返回 replay=%v err=%v", replay, err)
	}
	meta.RequestDigest = "digest-b"
	if _, _, err = store.Command(context.Background(), meta, create); !errors.Is(err, domain.ErrIdempotency) {
		t.Fatalf("同键异参应冲突：%v", err)
	}
}
