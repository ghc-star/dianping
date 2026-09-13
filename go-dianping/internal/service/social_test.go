package service

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/learning/go-dianping/internal/model"
	"github.com/redis/go-redis/v9"
)

func TestFeedCursorKeepsSameMillisecondAcrossThreePages(t *testing.T) {
	max, offset := int64(1000), 0
	for page := 1; page <= 3; page++ {
		max, offset = nextSocialCursor([]redis.Z{{Score: 1000}, {Score: 1000}}, max, offset)
		if max != 1000 || offset != page*2 {
			t.Fatalf("page %d cursor = (%d,%d), want (1000,%d)", page, max, offset, page*2)
		}
	}
	max, offset = nextSocialCursor([]redis.Z{{Score: 1000}, {Score: 999}}, max, offset)
	if max != 999 || offset != 1 {
		t.Fatalf("new timestamp must reset offset: got (%d,%d)", max, offset)
	}
}

func TestFeedCursorCountsOnlyTrailingTimestamp(t *testing.T) {
	tests := []struct {
		name       string
		scores     []redis.Z
		max        int64
		offset     int
		wantMax    int64
		wantOffset int
	}{
		{"older page", []redis.Z{{Score: 100}, {Score: 100}}, 200, 7, 100, 2},
		{"mixed page", []redis.Z{{Score: 100}, {Score: 99}}, 100, 5, 99, 1},
		{"empty page", nil, 123, 4, 123, 4},
		{"epoch score", []redis.Z{{Score: 0}}, 0, 1, 0, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			max, offset := nextSocialCursor(tt.scores, tt.max, tt.offset)
			if max != tt.wantMax || offset != tt.wantOffset {
				t.Fatalf("got (%d,%d), want (%d,%d)", max, offset, tt.wantMax, tt.wantOffset)
			}
		})
	}
}

func TestBlogValidationUsesCharactersAndRejectsBrokenImages(t *testing.T) {
	valid := model.Blog{ShopID: 1, Title: strings.Repeat("学", 255), Content: "Go 后端学习", Images: "/blogs/a.png"}
	if err := validateSocialBlog(&valid); err != nil {
		t.Fatalf("255 Chinese characters must fit varchar(255): %v", err)
	}
	tests := []struct {
		name string
		edit func(*model.Blog)
	}{
		{"missing shop", func(b *model.Blog) { b.ShopID = 0 }},
		{"blank title", func(b *model.Blog) { b.Title = "  " }},
		{"long title", func(b *model.Blog) { b.Title += "学" }},
		{"blank content", func(b *model.Blog) { b.Content = "\n" }},
		{"long content", func(b *model.Blog) { b.Content = strings.Repeat("学", 2049) }},
		{"empty image", func(b *model.Blog) { b.Images = "" }},
		{"empty middle image", func(b *model.Blog) { b.Images = "a,,b" }},
		{"too many images", func(b *model.Blog) { b.Images = strings.TrimSuffix(strings.Repeat("a,", 10), ",") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blog := valid
			tt.edit(&blog)
			if err := validateSocialBlog(&blog); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if err := validateSocialBlog(nil); err == nil {
		t.Fatal("nil blog must be rejected")
	}
}

func TestSocialOperationsValidateBeforeStorage(t *testing.T) {
	svc := NewSocial(nil, nil, nil)
	ctx := context.Background()
	if _, err := svc.HotBlogs(ctx, 0, 0); err == nil {
		t.Fatal("invalid page accepted")
	}
	if err := svc.Follow(ctx, 1, 1, true); err == nil {
		t.Fatal("self follow accepted")
	}
	if err := svc.LikeBlog(ctx, 0, 1); err == nil {
		t.Fatal("unauthenticated like accepted")
	}
	if _, err := svc.Feed(ctx, 1, 1000, -1); err == nil {
		t.Fatal("negative cursor offset accepted")
	}
	if _, err := svc.BlogByID(ctx, -1, 1); err == nil {
		t.Fatal("negative blog ID accepted")
	}
}

func TestSocialSetIntersectionAndMemberValidation(t *testing.T) {
	got := socialIntersection([]int64{1, 2, 2, 3}, []int64{3, 3, 2, 4})
	if !reflect.DeepEqual(got, []int64{3, 2}) {
		t.Fatalf("intersection must be deduplicated, got %v", got)
	}
	if got := socialIntersection(nil, nil); got == nil || len(got) != 0 {
		t.Fatal("empty collection must JSON-encode as []")
	}
	for _, invalid := range []string{"abc", "0", "-1", "9223372036854775808"} {
		if _, err := parseSocialIDs([]string{invalid}); err == nil {
			t.Fatalf("accepted invalid Redis ID %q", invalid)
		}
	}
}

func TestSocialWorkerStopsWhenContextAlreadyCancelled(t *testing.T) {
	svc := NewSocial(nil, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.Run(ctx)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker ignored shutdown")
	}
}
