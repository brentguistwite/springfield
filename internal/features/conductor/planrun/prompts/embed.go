package prompts

import _ "embed"

//go:embed header.tmpl
var HeaderTmpl string

//go:embed footer.tmpl
var FooterTmpl string
