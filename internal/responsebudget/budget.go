package responsebudget

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

type Page struct {
	Text       string
	Truncated  bool
	NextCursor string
}

type Position struct {
	Offset     int `json:"offset"`
	CharOffset int `json:"char_offset,omitempty"`
}

type ItemsPage[T any] struct {
	Items           []T
	NextCursor      string
	Truncated       bool
	BudgetTruncated bool
}

func Apply(text string, cursor string, limit int) (Page, error) {
	offset, err := DecodeOffset(cursor)
	if err != nil {
		return Page{}, err
	}
	runes := []rune(text)
	if offset > len(runes) {
		offset = len(runes)
	}
	if limit <= 0 || offset+limit >= len(runes) {
		return Page{Text: string(runes[offset:])}, nil
	}
	next := offset + limit
	return Page{
		Text:       string(runes[offset:next]),
		Truncated:  true,
		NextCursor: EncodeOffset(next),
	}, nil
}

func EncodeOffset(offset int) string {
	if offset <= 0 {
		return ""
	}
	return EncodePosition(offset, 0)
}

func EncodePosition(offset int, charOffset int) string {
	if offset < 0 {
		offset = 0
	}
	if charOffset < 0 {
		charOffset = 0
	}
	data, _ := json.Marshal(Position{Offset: offset, CharOffset: charOffset})
	return base64.RawURLEncoding.EncodeToString(data)
}

func DecodeOffset(cursor string) (int, error) {
	position, err := DecodePosition(cursor)
	if err != nil {
		return 0, err
	}
	return position.Offset, nil
}

func DecodePosition(cursor string) (Position, error) {
	if cursor == "" {
		return Position{}, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return Position{}, fmt.Errorf("invalid cursor %q", cursor)
	}
	var state Position
	if err := json.Unmarshal(data, &state); err != nil {
		return Position{}, fmt.Errorf("invalid cursor %q", cursor)
	}
	if state.Offset < 0 || state.CharOffset < 0 {
		return Position{}, fmt.Errorf("invalid cursor %q", cursor)
	}
	return state, nil
}

func ApplyItems[T any](items []T, cursor string, nextCursor string, limit int, render func([]T, string, bool, bool) ([]byte, error)) (ItemsPage[T], error) {
	position, err := DecodePosition(cursor)
	if err != nil {
		return ItemsPage[T]{}, err
	}
	if position.CharOffset != 0 {
		return ItemsPage[T]{}, fmt.Errorf("invalid cursor %q", cursor)
	}
	if limit <= 0 {
		return ItemsPage[T]{
			Items:      items,
			NextCursor: nextCursor,
			Truncated:  nextCursor != "",
		}, nil
	}
	data, err := render(items, nextCursor, nextCursor != "", false)
	if err != nil {
		return ItemsPage[T]{}, err
	}
	if len([]rune(string(data))) <= limit {
		return ItemsPage[T]{
			Items:      items,
			NextCursor: nextCursor,
			Truncated:  nextCursor != "",
		}, nil
	}
	for n := len(items) - 1; n >= 0; n-- {
		next := EncodePosition(position.Offset+n, 0)
		data, err := render(items[:n], next, true, true)
		if err != nil {
			return ItemsPage[T]{}, err
		}
		if n == 0 {
			return ItemsPage[T]{}, fmt.Errorf("response budget too small for next item")
		}
		if len([]rune(string(data))) <= limit {
			return ItemsPage[T]{
				Items:           items[:n],
				NextCursor:      next,
				Truncated:       true,
				BudgetTruncated: true,
			}, nil
		}
	}
	return ItemsPage[T]{Items: items, NextCursor: nextCursor, Truncated: nextCursor != ""}, nil
}

func ApplyTextItems[T any](items []T, cursor string, nextCursor string, limit int, text func(T) string, withText func(T, string) T, render func([]T, string, bool, bool) ([]byte, error)) (ItemsPage[T], error) {
	position, err := DecodePosition(cursor)
	if err != nil {
		return ItemsPage[T]{}, err
	}
	if limit <= 0 {
		return ItemsPage[T]{
			Items:      trimFirstTextItem(items, position.CharOffset, text, withText),
			NextCursor: nextCursor,
			Truncated:  nextCursor != "",
		}, nil
	}

	included := make([]T, 0, len(items))
	for i, item := range items {
		globalOffset := position.Offset + i
		start := 0
		if i == 0 {
			start = position.CharOffset
		}
		runes := []rune(text(item))
		if start > len(runes) {
			start = len(runes)
		}
		fullItem := withText(item, string(runes[start:]))
		hypotheticalNext := ""
		if i < len(items)-1 || nextCursor != "" {
			hypotheticalNext = EncodePosition(globalOffset+1, 0)
		}
		candidate := append(append([]T(nil), included...), fullItem)
		data, err := render(candidate, hypotheticalNext, hypotheticalNext != "", hypotheticalNext != "")
		if err != nil {
			return ItemsPage[T]{}, err
		}
		if len([]rune(string(data))) <= limit {
			included = append(included, fullItem)
			continue
		}
		if len(included) > 0 {
			next := EncodePosition(globalOffset, start)
			return ItemsPage[T]{
				Items:           included,
				NextCursor:      next,
				Truncated:       true,
				BudgetTruncated: true,
			}, nil
		}
		best := 0
		low, high := 1, len(runes)-start
		for low <= high {
			mid := (low + high) / 2
			next := EncodePosition(globalOffset, start+mid)
			partial := []T{withText(item, string(runes[start:start+mid]))}
			data, err := render(partial, next, true, true)
			if err != nil {
				return ItemsPage[T]{}, err
			}
			if len([]rune(string(data))) <= limit {
				best = mid
				low = mid + 1
			} else {
				high = mid - 1
			}
		}
		if best > 0 {
			next := EncodePosition(globalOffset, start+best)
			return ItemsPage[T]{
				Items:           []T{withText(item, string(runes[start:start+best]))},
				NextCursor:      next,
				Truncated:       true,
				BudgetTruncated: true,
			}, nil
		}
		return ItemsPage[T]{}, fmt.Errorf("response budget too small for next item")
	}

	data, err := render(included, nextCursor, nextCursor != "", false)
	if err != nil {
		return ItemsPage[T]{}, err
	}
	if len([]rune(string(data))) <= limit {
		return ItemsPage[T]{
			Items:      included,
			NextCursor: nextCursor,
			Truncated:  nextCursor != "",
		}, nil
	}
	return ApplyItems(included, EncodePosition(position.Offset, 0), nextCursor, limit, render)
}

func trimFirstTextItem[T any](items []T, charOffset int, text func(T) string, withText func(T, string) T) []T {
	if len(items) == 0 || charOffset <= 0 {
		return items
	}
	out := append([]T(nil), items...)
	runes := []rune(text(out[0]))
	if charOffset > len(runes) {
		charOffset = len(runes)
	}
	out[0] = withText(out[0], string(runes[charOffset:]))
	return out
}
