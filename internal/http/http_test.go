package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"text/template"

	"github.com/avanha/pmaas-plugin-groups/data"
	spi "github.com/avanha/pmaas-spi"
)

func render(t *testing.T, name string, value any) string {
	t.Helper()

	compiled, err := template.New(name).Funcs(templateInfo(name).FuncMap).ParseFS(contentFS, "content/templates/"+name+".htmlt")
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := compiled.ExecuteTemplate(&out, name+".htmlt", value); err != nil {
		t.Fatal(err)
	}

	return out.String()
}

func TestHeader_HasBreadcrumbsAndChildGroupsWithCounts(t *testing.T) {
	out := render(t, "groups_header", data.Header{
		Crumbs:   []data.Crumb{{Label: "All", Href: "/plugins/groups/"}, {Label: "Cougar"}},
		Children: []data.ChildGroup{{Name: "Master Bedroom", Href: "/plugins/groups/?g=Cougar%2FMaster+Bedroom", Groups: 0, Entities: 1}, {Name: "Upstairs", Href: "/x", Groups: 2, Entities: 5}},
		Notes:    []string{"1 configured entity is not registered"},
	})

	for _, want := range []string{
		`<a href="/plugins/groups/">All</a>`, `<span class="here">Cougar</span>`, `href="/plugins/groups/?g=Cougar%2FMaster+Bedroom"`,
		"1 item<", "2 groups, 5 items", "1 configured entity is not registered",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}

	if strings.Contains(out, "Nothing is in this group") {
		t.Error("said it was empty")
	}
}

func TestHeader_SaysSoWhenTheGroupIsEmpty(t *testing.T) {
	if out := render(t, "groups_header", data.Header{Crumbs: []data.Crumb{{Label: "All"}}, Empty: true}); !strings.Contains(out, "Nothing is in this group") {
		t.Error(out)
	}
}

func TestDividerAndSummary_NamesCannotBecomeMarkup(t *testing.T) {
	for name, value := range map[string]any{
		"groups_header":  data.Header{Crumbs: []data.Crumb{{Label: "<script>x</script>"}}, Children: []data.ChildGroup{{Name: "<script>x</script>"}}, Notes: []string{"<script>x</script>"}},
		"groups_divider": data.Divider{Text: "<script>x</script>", Href: `"><script>x</script>`},
		"groups_summary": data.Summary{Name: "<script>x</script>", Type: "<b>", Plugin: "<i>", PluginHref: `"><x>`, Error: "<script>x</script>"},
	} {
		if strings.Contains(render(t, name, value), "<script>x") {
			t.Errorf("%s printed markup unescaped", name)
		}
	}
}

func TestSummary_NamesTheEntityItsTypeAndWhereItsPluginIs(t *testing.T) {
	out := render(t, "groups_summary", data.Summary{Name: "Pool Pump", Type: "device.ISwitch", Plugin: "esphome", PluginHref: "/plugins/esphome/"})

	for _, want := range []string{"Pool Pump", "device.ISwitch", `<a href="/plugins/esphome/">esphome</a>`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
}

type fakePages map[string]data.Page

func (f fakePages) Page(path string) (data.Page, bool) {
	page, ok := f[path]

	return page, ok
}

type listContainer struct {
	spi.IPMAASContainer

	title string
	items []any
	head  any
}

func (c *listContainer) RenderList(_ http.ResponseWriter, _ *http.Request, options spi.RenderListOptions, items []any) {
	c.title, c.head, c.items = options.Title, options.Header, items
}

func TestHandler_RendersTheGroupAskedForAndSaysNotFoundForOthers(t *testing.T) {
	container := &listContainer{}
	h := &Handler{container: container, pages: fakePages{
		"":       {Title: "All", Header: data.Header{Empty: true}},
		"Cougar": {Title: "Cougar", Items: []any{"card"}},
	}}

	w := httptest.NewRecorder()
	h.handleList(w, httptest.NewRequest(http.MethodGet, "/plugins/groups/?g=Cougar", nil))

	if container.title != "Cougar" || len(container.items) != 1 {
		t.Errorf("%q %v", container.title, container.items)
	}

	h.handleList(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/plugins/groups/", nil))

	if container.title != "All" {
		t.Errorf("the root was not shown for no group: %q", container.title)
	}

	w = httptest.NewRecorder()
	h.handleList(w, httptest.NewRequest(http.MethodGet, "/plugins/groups/?g=Nowhere", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("%d", w.Code)
	}
}
