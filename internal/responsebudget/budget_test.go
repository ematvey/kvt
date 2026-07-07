package responsebudget

import (
	"strings"
	"testing"
)

func TestApplyReturnsExplicitCursor(t *testing.T) {
	page, err := Apply("abcdef", "", 3)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if page.Text != "abc" || !page.Truncated || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}

	next, err := Apply("abcdef", page.NextCursor, 3)
	if err != nil {
		t.Fatalf("Apply next: %v", err)
	}
	if next.Text != "def" || next.Truncated || next.NextCursor != "" {
		t.Fatalf("next = %#v", next)
	}
}

func TestApplyItemsRejectsNoProgressCursorWhenBudgetTooSmall(t *testing.T) {
	_, err := ApplyItems([]string{"oversized"}, "", "", 1, func(items []string, next string, truncated bool, budgetTruncated bool) ([]byte, error) {
		return []byte(`{"items":[],"next_cursor":"` + next + `"}`), nil
	})
	if err == nil {
		t.Fatalf("expected too-small budget error")
	}
}

func TestApplyTextItemsRejectsNoProgressCursorWhenBudgetTooSmall(t *testing.T) {
	_, err := ApplyTextItems([]string{"oversized"}, "", "", 1,
		func(item string) string {
			return item
		},
		func(_ string, text string) string {
			return text
		},
		func(items []string, next string, truncated bool, budgetTruncated bool) ([]byte, error) {
			return []byte(`{"items":[],"next_cursor":"` + next + `"}`), nil
		},
	)
	if err == nil {
		t.Fatalf("expected too-small budget error")
	}
}

func TestApplyTextItemsAdvancesWithinOversizedItem(t *testing.T) {
	text := strings.Repeat("abcdefghijklmnopqrstuvwxyz", 8)
	page, err := ApplyTextItems([]string{text}, "", "", 95,
		func(item string) string {
			return item
		},
		func(_ string, text string) string {
			return text
		},
		func(items []string, next string, truncated bool, budgetTruncated bool) ([]byte, error) {
			text := ""
			if len(items) > 0 {
				text = items[0]
			}
			return []byte(`{"text":"` + text + `","next_cursor":"` + next + `"}`), nil
		},
	)
	if err != nil {
		t.Fatalf("ApplyTextItems: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0] == "" || page.Items[0] == text || page.NextCursor == "" {
		t.Fatalf("page = %#v", page)
	}
	combined := page.Items[0]
	cursor := page.NextCursor
	for i := 0; cursor != "" && i < 20; i++ {
		next, err := ApplyTextItems([]string{text}, cursor, "", 120,
			func(item string) string {
				return item
			},
			func(_ string, text string) string {
				return text
			},
			func(items []string, next string, truncated bool, budgetTruncated bool) ([]byte, error) {
				text := ""
				if len(items) > 0 {
					text = items[0]
				}
				return []byte(`{"text":"` + text + `","next_cursor":"` + next + `"}`), nil
			},
		)
		if err != nil {
			t.Fatalf("ApplyTextItems next: %v", err)
		}
		if len(next.Items) != 1 {
			t.Fatalf("next = %#v", next)
		}
		combined += next.Items[0]
		cursor = next.NextCursor
	}
	if cursor != "" || combined != text {
		t.Fatalf("combined len=%d cursor=%q, want len=%d", len(combined), cursor, len(text))
	}
}
