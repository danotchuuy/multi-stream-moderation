package server

import (
	"reflect"
	"testing"
)

type historyTestMsg struct {
	id   string
	text string
}

func TestChatHistoryCacheRecentIsOldestFirstAndCapped(t *testing.T) {
	c := newChatHistoryCache[string, historyTestMsg](3, func(m historyTestMsg) string { return m.id })

	for _, id := range []string{"1", "2", "3", "4"} {
		c.add("chan", historyTestMsg{id: id, text: "msg " + id})
	}

	got := c.recent("chan")
	want := []historyTestMsg{{id: "2", text: "msg 2"}, {id: "3", text: "msg 3"}, {id: "4", text: "msg 4"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recent = %+v, want %+v", got, want)
	}
}

func TestChatHistoryCacheAddSkipsDuplicateID(t *testing.T) {
	c := newChatHistoryCache[string, historyTestMsg](10, func(m historyTestMsg) string { return m.id })

	// Simulates two viewers of the same Twitch channel each reporting
	// their own copy of the same EventSub notification.
	c.add("chan", historyTestMsg{id: "1", text: "first copy"})
	c.add("chan", historyTestMsg{id: "1", text: "second copy"})

	got := c.recent("chan")
	want := []historyTestMsg{{id: "1", text: "first copy"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("recent = %+v, want %+v", got, want)
	}
}

func TestChatHistoryCacheRecentIsIsolatedPerKey(t *testing.T) {
	c := newChatHistoryCache[string, historyTestMsg](10, func(m historyTestMsg) string { return m.id })
	c.add("chan-a", historyTestMsg{id: "1"})

	if got := c.recent("chan-b"); len(got) != 0 {
		t.Errorf("recent(chan-b) = %+v, want empty", got)
	}
}

func TestChatHistoryCacheRecentReturnsACopy(t *testing.T) {
	c := newChatHistoryCache[string, historyTestMsg](10, func(m historyTestMsg) string { return m.id })
	c.add("chan", historyTestMsg{id: "1"})

	got := c.recent("chan")
	got[0].text = "mutated"

	if again := c.recent("chan"); again[0].text == "mutated" {
		t.Error("recent's caller was able to mutate the cache's internal state")
	}
}
