package dispatch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// fakeSwapper records the label calls in order and can fail the nth one.
type fakeSwapper struct {
	calls  []string
	failAt int // 1-based call number to fail, 0 = never
	err    error

	owner, repo string
	number      int
}

func (f *fakeSwapper) record(call string) error {
	f.calls = append(f.calls, call)
	if f.failAt != 0 && len(f.calls) == f.failAt {
		return f.err
	}
	return nil
}

func (f *fakeSwapper) AddLabels(_ context.Context, owner, repo string, number int, labels []string) ([]string, error) {
	f.owner, f.repo, f.number = owner, repo, number
	return labels, f.record(fmt.Sprintf("add %v", labels))
}

func (f *fakeSwapper) RemoveLabel(_ context.Context, owner, repo string, number int, label string) error {
	f.owner, f.repo, f.number = owner, repo, number
	return f.record("remove " + label)
}

func TestRedispatch_CallOrder(t *testing.T) {
	f := &fakeSwapper{}
	labels := []string{"ai-implement", "ai-review-ai-merge"}

	if err := Redispatch(context.Background(), f, "freaxnx01", "bridge", 43, labels); err != nil {
		t.Fatalf("Redispatch: %v", err)
	}

	want := []string{
		"add [ai:retrying]",
		"remove ai-implement",
		"add [ai-implement ai-review-ai-merge]",
		"remove ai:retrying",
	}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls =\n  %v\nwant\n  %v", f.calls, want)
	}
	if f.owner != "freaxnx01" || f.repo != "bridge" || f.number != 43 {
		t.Errorf("target = %s/%s#%d", f.owner, f.repo, f.number)
	}
}

func TestRedispatch_StopsAtFailingStep(t *testing.T) {
	boom := errors.New("boom")

	for failAt := 1; failAt <= 4; failAt++ {
		t.Run(fmt.Sprintf("step %d", failAt), func(t *testing.T) {
			f := &fakeSwapper{failAt: failAt, err: boom}

			err := Redispatch(context.Background(), f, "freaxnx01", "bridge", 43,
				[]string{"ai-implement"})

			if err == nil {
				t.Fatal("want an error")
			}
			if len(f.calls) != failAt {
				t.Errorf("the sequence must stop at the failing call: %v", f.calls)
			}
			var re *RedispatchError
			if !errors.As(err, &re) {
				t.Fatalf("want a *RedispatchError, got %T: %v", err, err)
			}
			if re.Step != failAt {
				t.Errorf("Step = %d want %d", re.Step, failAt)
			}
			if !errors.Is(err, boom) {
				t.Errorf("the cause must stay reachable: %v", err)
			}
			if want := fmt.Sprintf("step %d", failAt); !strings.Contains(err.Error(), want) {
				t.Errorf("error %q must name %q", err, want)
			}
		})
	}
}
