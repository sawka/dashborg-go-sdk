package dash

const OPTION_ONLOADHANDLER = "onloadhandler"
const OPTION_HTML = "html"
const OPTION_AUTH = "auth"

type AppOption interface {
	OptionName() string
	OptionData() interface{}
}

type OnloadHandlerOption struct {
	Path string `json:"path"`
}

func (opt OnloadHandlerOption) OptionName() string {
	return OPTION_ONLOADHANDLER
}

func (opt OnloadHandlerOption) OptionData() interface{} {
	return opt
}

type HtmlOption struct {
	Type string `json:"type"`
}

func (opt HtmlOption) OptionName() string {
	return OPTION_HTML
}

func (opt HtmlOption) OptionData() interface{} {
	return opt
}

type AuthOption struct {
	Type string `json:"type"`
}

func (opt AuthOption) OptionName() string {
	return OPTION_AUTH
}

func (opt AuthOption) OptionData() interface{} {
	return opt
}
