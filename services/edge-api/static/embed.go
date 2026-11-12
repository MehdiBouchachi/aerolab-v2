// Package static embeds the default landing page. The real web application
// (apps/web, Phase 2) is built separately and served from AEROLAB_STATIC_DIR.
package static

import "embed"

//go:embed index.html
var FS embed.FS
