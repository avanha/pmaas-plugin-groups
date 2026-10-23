// Package http is the groups plugin's page: the route, and the renderers for what is on it.
package http

import (
	"embed"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"text/template"

	"github.com/avanha/pmaas-plugin-groups/data"
	spi "github.com/avanha/pmaas-spi"
)

//go:embed content/static content/templates
var contentFS embed.FS

// The templates are rendered by PMAAS's template engine plugin, which uses text/template: it does no HTML escaping, so every string a
// template prints has to go through the html function itself.
func templateInfo(name string) *spi.TemplateInfo {
	return &spi.TemplateInfo{
		Name:    name,
		FuncMap: template.FuncMap{},
		Paths:   []string{"templates/" + name + ".htmlt"},
		Styles:  []string{"css/groups.css"},
		Scripts: []string{"js/groups.js"},
	}
}

// Pages makes the pages.
type Pages interface {
	// Page is the page of the group at the path, or false if there is none.
	Page(path string) (data.Page, bool)
}

// MenuPriority is where the plugin is in the menu: with the pinned entries, below Status, Alerts and Automations.
const MenuPriority = 70

type Handler struct {
	container spi.IPMAASContainer
	pages     Pages
}

func NewHandler() *Handler {
	return &Handler{}
}

func (h *Handler) Init(container spi.IPMAASContainer, pages Pages, menuLabel string) {
	h.container = container
	h.pages = pages

	container.ProvideContentFS(&contentFS, "content")
	container.EnableStaticContent("static")
	container.AddRouteWithOptions("", h.handleList,
		&spi.HttpHandlerOptions{SupportsXsrfValidation: true, MenuLabel: menuLabel, MenuPriority: MenuPriority})
	container.RegisterEntityRenderer(reflect.TypeFor[data.Header](), h.rendererFactory("groups_header", func(v any) (any, bool) { x, ok := v.(data.Header); return x, ok }))
	container.RegisterEntityRenderer(reflect.TypeFor[data.Divider](), h.rendererFactory("groups_divider", func(v any) (any, bool) { x, ok := v.(data.Divider); return x, ok }))
	container.RegisterEntityRenderer(reflect.TypeFor[data.Summary](), h.rendererFactory("groups_summary", func(v any) (any, bool) { x, ok := v.(data.Summary); return x, ok }))
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	page, ok := h.pages.Page(r.URL.Query().Get("g"))
	if !ok {
		http.NotFound(w, r)

		return
	}

	h.container.RenderList(w, r, spi.RenderListOptions{Title: page.Title, Header: page.Header}, page.Items)
}

// rendererFactory is the renderer for one kind of thing on the page: its template, and the check that it is given the right type.
func (h *Handler) rendererFactory(name string, as func(any) (any, bool)) spi.EntityRendererFactory {
	info := templateInfo(name)

	return func() (spi.EntityRenderer, error) {
		compiled, err := h.container.GetTemplate(info)
		if err != nil {
			return spi.EntityRenderer{}, fmt.Errorf("unable to load the %s template: %w", name, err)
		}

		render := func(w io.Writer, entity any) error {
			value, ok := as(entity)
			if !ok {
				return fmt.Errorf("item is not what the %s template draws: %T", name, entity)
			}

			if err := compiled.Instance.Execute(w, value); err != nil {
				return fmt.Errorf("unable to execute the %s template: %w", name, err)
			}

			return nil
		}

		return spi.EntityRenderer{StreamingRenderFunc: render, Styles: compiled.Styles, Scripts: compiled.Scripts}, nil
	}
}
