package admin

import (
	"bytes"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/richardmcquiston01/url-shortener/web"
)

// pageTemplate is one page parsed together with the shared base layout.
type pageTemplate struct {
	template *template.Template
}

// page is the data every template receives; Content holds the page's own data.
type page struct {
	Title         string
	CSRFToken     string
	Authenticated bool
	Content       any
}

type errorContent struct {
	Heading   string
	Message   string
	BackURL   string
	BackLabel string
}

type loginContent struct {
	Error string
}

var pageNames []string = []string{"login", "dashboard", "error"}

func parsePages(baseURL string) (map[string]*pageTemplate, error) {
	functions := template.FuncMap{
		"formatTime": func(moment time.Time) string { return moment.UTC().Format("2006-01-02 15:04 UTC") },
		"shortURL":   func(slug string) string { return baseURL + "/" + slug },
	}

	pages := make(map[string]*pageTemplate, len(pageNames))
	for _, name := range pageNames {
		parsed, err := template.New("base").Funcs(functions).
			ParseFS(web.Files, "templates/base.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse %q admin template: %w", name, err)
		}
		pages[name] = &pageTemplate{template: parsed}
	}
	return pages, nil
}

// render executes the page into a buffer first, so a template failure becomes a
// clean 500 instead of a half-written page.
func (handler *Handler) render(responseWriter http.ResponseWriter, status int, name string, current *session, title string, content any) {
	data := page{Title: title, Content: content}
	if current != nil {
		data.CSRFToken = current.CSRFToken
		data.Authenticated = current.Authenticated
	}

	var buffer bytes.Buffer
	if err := handler.pages[name].template.ExecuteTemplate(&buffer, "base", data); err != nil {
		handler.logger.Error("render admin page", "page", name, "error", err)
		http.Error(responseWriter, "something went wrong while rendering this page", http.StatusInternalServerError)
		return
	}
	responseWriter.Header().Set("Content-Type", "text/html; charset=utf-8")
	responseWriter.WriteHeader(status)
	_, _ = buffer.WriteTo(responseWriter)
}

func (handler *Handler) renderError(responseWriter http.ResponseWriter, current *session, status int, heading string, message string, backURL string, backLabel string) {
	handler.render(responseWriter, status, "error", current, heading,
		errorContent{Heading: heading, Message: message, BackURL: backURL, BackLabel: backLabel})
}
