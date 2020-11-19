package dashtmpl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"
	"text/template"

	"github.com/sawka/dashborg-go-sdk/pkg/dash"
)

var AttrRe *regexp.Regexp = regexp.MustCompile("^[A-Za-z][A-Za-z0-9_]*$")

type HasPrintMulti interface {
	PrintMulti(string)
}

func jsonAttr(attrName string, v interface{}) (string, error) {
	if attrName == "" || v == nil {
		return "", nil
	}
	if !AttrRe.MatchString(attrName) {
		return "", fmt.Errorf("Invalid Attribute Name %s", attrName)
	}
	barr, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	strVal := string(barr)
	strVal = strings.ReplaceAll(strVal, "\\", "\\\\")
	strVal = strings.ReplaceAll(strVal, "\"", "\\\"")
	return fmt.Sprintf(" @%s=\"%s\"", attrName, strVal), nil
}

func NewTemplate(name string) *template.Template {
	tmpl := template.New(name)
	tmpl.Funcs(map[string]interface{}{"jsonattr": jsonAttr})
	return tmpl
}

func PrintTemplateStr(builder HasPrintMulti, tmplStr string, data interface{}) error {
	tmpl := NewTemplate("root")
	tmpl, err := tmpl.Parse(tmplStr)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, data)
	if err != nil {
		return err
	}
	builder.PrintMulti(buf.String())
	return nil
}

func PrintTemplate(builder HasPrintMulti, t *template.Template, tmplName string, data interface{}) error {
	var buf bytes.Buffer
	err := t.ExecuteTemplate(&buf, tmplName, data)
	if err != nil {
		return err
	}
	builder.PrintMulti(buf.String())
	return nil
}

func DefinePanelFromTemplate(panelName string, t *template.Template, tmplName string, data interface{}) (*dash.Panel, error) {
	if dash.Client == nil {
		panic("Dashborg not initialized, must call dash.StartProcClient()")
	}
	rtnPanel := &dash.Panel{PanelName: panelName}
	pw := dash.DefinePanel(panelName)
	err := PrintTemplate(pw, t, tmplName, data)
	if err != nil {
		return rtnPanel, err
	}
	return pw.Flush()
}

func RenderContextTemplate(p *dash.Panel, contextName string, fn func(req *dash.PanelRequest) (interface{}, error), t *template.Template) {
	ctx := p.LookupControl("context", contextName)
	if !ctx.IsValid() {
		log.Printf("Invalid context '%s' for RenderContextTemplate", contextName)
		return
	}
	ctx.OnContextRequest(func(cw *dash.ContextWriter, req *dash.PanelRequest) error {
		data, err := fn(req)
		if err != nil {
			return err
		}
		err = PrintTemplate(cw, t, contextName, data)
		if err != nil {
			return err
		}
		cw.Flush()
		return nil
	})
}
