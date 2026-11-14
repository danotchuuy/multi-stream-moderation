package server

import (
	"encoding/json"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/danotchuuy/multi-stream-moderation/internal/youtubepb"
)

func youtubeTestItem(id string, snippet *youtubepb.LiveChatMessageSnippet) *youtubepb.LiveChatMessage {
	snippet.PublishedAt = proto.String("2026-11-14T20:00:00Z")
	return &youtubepb.LiveChatMessage{
		Id:      proto.String(id),
		Snippet: snippet,
		AuthorDetails: &youtubepb.LiveChatMessageAuthorDetails{
			DisplayName: proto.String("Fan"),
			ChannelId:   proto.String("UCfan"),
		},
	}
}

func TestYouTubeChatEventFromItemSuperChat(t *testing.T) {
	item := youtubeTestItem("sc1", &youtubepb.LiveChatMessageSnippet{
		Type: youtubepb.LiveChatMessageSnippet_TypeWrapper_SUPER_CHAT_EVENT.Enum(),
		DisplayedContent: &youtubepb.LiveChatMessageSnippet_SuperChatDetails{
			SuperChatDetails: &youtubepb.LiveChatSuperChatDetails{
				AmountDisplayString: proto.String("$5.00"),
				UserComment:         proto.String("great stream"),
				Tier:                proto.Uint32(2),
			},
		},
	})

	ev, ok := youtubeChatEventFromItem(item)
	if !ok || ev.Name != "superchat" {
		t.Fatalf("got (%q, %v), want a superchat event", ev.Name, ok)
	}
	var got youtubeSuperChatDTO
	if err := json.Unmarshal(ev.Data, &got); err != nil {
		t.Fatal(err)
	}
	want := youtubeSuperChatDTO{
		ID: "sc1", DisplayName: "Fan", ChannelID: "UCfan", Amount: "$5.00",
		Comment: "great stream", Tier: 2, Timestamp: 1794686400000,
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestYouTubeChatEventFromItemSuperSticker(t *testing.T) {
	item := youtubeTestItem("st1", &youtubepb.LiveChatMessageSnippet{
		Type: youtubepb.LiveChatMessageSnippet_TypeWrapper_SUPER_STICKER_EVENT.Enum(),
		DisplayedContent: &youtubepb.LiveChatMessageSnippet_SuperStickerDetails{
			SuperStickerDetails: &youtubepb.LiveChatSuperStickerDetails{
				AmountDisplayString: proto.String("$2.00"),
			},
		},
	})

	ev, ok := youtubeChatEventFromItem(item)
	if !ok || ev.Name != "superchat" {
		t.Fatalf("got (%q, %v), want a superchat event", ev.Name, ok)
	}
	var got youtubeSuperChatDTO
	if err := json.Unmarshal(ev.Data, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Sticker || got.Amount != "$2.00" || got.Comment != "" {
		t.Errorf("got %+v, want a $2.00 sticker with no comment", got)
	}
}

func TestYouTubeChatEventFromItemMemberships(t *testing.T) {
	newMember := youtubeTestItem("m1", &youtubepb.LiveChatMessageSnippet{
		Type: youtubepb.LiveChatMessageSnippet_TypeWrapper_NEW_SPONSOR_EVENT.Enum(),
		DisplayedContent: &youtubepb.LiveChatMessageSnippet_NewSponsorDetails{
			NewSponsorDetails: &youtubepb.LiveChatNewSponsorDetails{
				MemberLevelName: proto.String("Gold"),
				IsUpgrade:       proto.Bool(true),
			},
		},
	})
	milestone := youtubeTestItem("m2", &youtubepb.LiveChatMessageSnippet{
		Type: youtubepb.LiveChatMessageSnippet_TypeWrapper_MEMBER_MILESTONE_CHAT_EVENT.Enum(),
		DisplayedContent: &youtubepb.LiveChatMessageSnippet_MemberMilestoneChatDetails{
			MemberMilestoneChatDetails: &youtubepb.LiveChatMemberMilestoneChatDetails{
				MemberLevelName: proto.String("Gold"),
				MemberMonth:     proto.Uint32(6),
				UserComment:     proto.String("half a year!"),
			},
		},
	})

	for _, tc := range []struct {
		item *youtubepb.LiveChatMessage
		want youtubeMembershipDTO
	}{
		{newMember, youtubeMembershipDTO{ID: "m1", DisplayName: "Fan", ChannelID: "UCfan", Level: "Gold", Upgrade: true, Timestamp: 1794686400000}},
		{milestone, youtubeMembershipDTO{ID: "m2", DisplayName: "Fan", ChannelID: "UCfan", Level: "Gold", Months: 6, Comment: "half a year!", Timestamp: 1794686400000}},
	} {
		ev, ok := youtubeChatEventFromItem(tc.item)
		if !ok || ev.Name != "membership" {
			t.Fatalf("%s: got (%q, %v), want a membership event", tc.want.ID, ev.Name, ok)
		}
		var got youtubeMembershipDTO
		if err := json.Unmarshal(ev.Data, &got); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.want.ID, got, tc.want)
		}
	}
}
