package server

import (
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestYoutubeNextReconnectDelayGrowsOnRepeatedQuickFailures(t *testing.T) {
	err := status.Error(codes.Unavailable, "unavailable")

	first := youtubeNextReconnectDelay(0, 0, err)
	if first != youtubeStreamReconnectDelay {
		t.Errorf("first delay = %v, want base delay %v", first, youtubeStreamReconnectDelay)
	}

	second := youtubeNextReconnectDelay(first, 0, err)
	if second != first*2 {
		t.Errorf("second delay = %v, want %v (double the first)", second, first*2)
	}

	// Growth should stop at the cap, not overshoot it.
	huge := youtubeStreamMaxReconnectDelay * 10
	capped := youtubeNextReconnectDelay(huge, 0, err)
	if capped != youtubeStreamMaxReconnectDelay {
		t.Errorf("capped delay = %v, want the max %v", capped, youtubeStreamMaxReconnectDelay)
	}
}

func TestYoutubeNextReconnectDelayResetsAfterAHealthyStream(t *testing.T) {
	err := status.Error(codes.Unavailable, "unavailable")

	// A long-lived attempt failing should reset backoff to the base
	// delay, not keep compounding on whatever the previous delay was.
	delay := youtubeNextReconnectDelay(youtubeStreamMaxReconnectDelay, youtubeStreamHealthyDuration, err)
	if delay != youtubeStreamReconnectDelay {
		t.Errorf("delay after a healthy stream = %v, want base delay %v", delay, youtubeStreamReconnectDelay)
	}
}

func TestYoutubeNextReconnectDelayFloorsResourceExhausted(t *testing.T) {
	err := status.Error(codes.ResourceExhausted, "quota exceeded")

	// Even on the very first (instant) failure, a quota error shouldn't
	// retry at the base delay — that just re-hits the same quota limit
	// every few seconds for the rest of the day.
	delay := youtubeNextReconnectDelay(0, 0, err)
	if delay < youtubeStreamResourceExhaustedDelay {
		t.Errorf("delay = %v, want at least %v for RESOURCE_EXHAUSTED", delay, youtubeStreamResourceExhaustedDelay)
	}
}

func TestYoutubeNextReconnectDelayNonStatusErrorUsesBaseDelay(t *testing.T) {
	// io.EOF and similar aren't grpc status errors at all (see
	// youtubeStreamErrTerminal) but must still get a sane delay rather
	// than panicking or falling through to zero.
	delay := youtubeNextReconnectDelay(0, 0, errPlain("boom"))
	if delay != youtubeStreamReconnectDelay {
		t.Errorf("delay = %v, want base delay %v", delay, youtubeStreamReconnectDelay)
	}
}

type errPlain string

func (e errPlain) Error() string { return string(e) }
