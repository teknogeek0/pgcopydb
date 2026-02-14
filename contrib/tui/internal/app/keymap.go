package app

import "github.com/charmbracelet/bubbles/key"

type KeyMap struct {
	NextTab key.Binding
	PrevTab key.Binding
	Tab1    key.Binding
	Tab2    key.Binding
	Tab3    key.Binding
	Tab4    key.Binding
	Tab5    key.Binding
	Up      key.Binding
	Down    key.Binding
	Top     key.Binding
	Bottom  key.Binding
	Sort    key.Binding
	Filter  key.Binding
	Help    key.Binding
	Quit    key.Binding
}

var Keys = KeyMap{
	NextTab: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next tab")),
	PrevTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev tab")),
	Tab1:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "overview")),
	Tab2:    key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "source")),
	Tab3:    key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "target")),
	Tab4:    key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "tables")),
	Tab5:    key.NewBinding(key.WithKeys("5"), key.WithHelp("5", "system")),
	Up:      key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("k/up", "up")),
	Down:    key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("j/down", "down")),
	Top:     key.NewBinding(key.WithKeys("g"), key.WithHelp("g", "top")),
	Bottom:  key.NewBinding(key.WithKeys("G"), key.WithHelp("G", "bottom")),
	Sort:    key.NewBinding(key.WithKeys("s", "S"), key.WithHelp("s", "sort")),
	Filter:  key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
	Help:    key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Quit:    key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
}
