// Package data is what the groups plugin hands to its renderers: the header of a group's page, the dividers between the entities
// in it, and the card for an entity whose plugin gave no view of its own.
package data

// Crumb is one step of the way from the root to the group on show. The last has no Href: it is where the page is.
type Crumb struct {
	Label string
	Href  string
}

// ChildGroup is a group directly under the one on show.
type ChildGroup struct {
	Name string
	Href string

	// Groups is how many groups are directly under it, and Entities how many entities are in it and below it.
	Groups   int
	Entities int

	// Hue is the colour of the tile, 0 to 359, the same for the same name.
	Hue int
}

// Header is the top of a group's page: where it is, and the groups below it.
type Header struct {
	Crumbs   []Crumb
	Children []ChildGroup

	// Notes are things the person should know, such as that some of what is in the group isn't there yet.
	Notes []string

	// Empty is that there is nothing to show under the groups.
	Empty bool
}

// Divider heads the entities after it: a kind of entity (Fans), or, when Major, the entities of the groups below the one on show.
type Divider struct {
	Text  string
	Major bool

	// Hue is the colour of the heading, 0 to 359.
	Hue int
}

// Summary is the card for an entity whose plugin has no view of it: what it is called, what it is, and where its plugin's page is.
type Summary struct {
	Name       string
	Type       string
	Plugin     string
	PluginHref string

	// Error is why its view could not be made, if it could not.
	Error string
}

// Page is a group's page, for the handler to draw.
type Page struct {
	Title  string
	Header Header

	// Items are what is drawn under the header, in order: entities' views, Dividers and Summaries.
	Items []any
}
