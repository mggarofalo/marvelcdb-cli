package output

import "html"

// htmlUnescape decodes HTML entities such as &mdash; and &quot;.
func htmlUnescape(s string) string { return html.UnescapeString(s) }
