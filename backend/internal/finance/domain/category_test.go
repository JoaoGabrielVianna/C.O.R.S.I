package domain

import (
	"testing"

	"github.com/google/uuid"
)

func validCategory() Category {
	return Category{
		WorkspaceID: uuid.New(),
		Name:        "Groceries",
		Type:        EntryTypeExpense,
		Color:       "#a4f000",
		Icon:        "cart",
	}
}

func TestCategoryValidate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mutate  func(*Category)
		wantErr bool
	}{
		{"happy", func(*Category) {}, false},
		{"missing workspace", func(c *Category) { c.WorkspaceID = uuid.Nil }, true},
		{"blank name", func(c *Category) { c.Name = "  " }, true},
		{"long name", func(c *Category) { c.Name = string(make([]byte, 81)) }, true},
		{"bad type", func(c *Category) { c.Type = "bogus" }, true},
		{"bad color", func(c *Category) { c.Color = "blue" }, true},
		{"blank icon", func(c *Category) { c.Icon = "" }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCategory()
			tc.mutate(&c)
			err := c.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestCardValidate(t *testing.T) {
	t.Parallel()
	c := Card{
		WorkspaceID: uuid.New(),
		Name:        "Nubank",
		Institution: "Nu",
		Network:     CardNetworkVisa,
		Last4:       "1234",
		LimitCents:  100000,
		ClosingDay:  10,
		DueDay:      20,
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid card rejected: %v", err)
	}

	bad := c
	bad.Last4 = "12"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected last4 validation error")
	}

	bad = c
	bad.ClosingDay = 31
	if err := bad.Validate(); err == nil {
		t.Fatal("expected closing_day validation error")
	}
}

func TestEntryTypeValid(t *testing.T) {
	t.Parallel()
	if !EntryTypeIncome.Valid() || !EntryTypeExpense.Valid() {
		t.Fatal("known types should be valid")
	}
	if EntryType("transfer").Valid() {
		t.Fatal("transfer must not be a valid entry type in v0.1")
	}
}
