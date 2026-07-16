package manifest

// validIcons is the catalog of icon identifiers that meta.yaml's `icon` field
// may take. The string is a fully-translated semantic identifier — the
// marketplace frontend maps it to its own icon library (e.g. lucide-react).
//
// Documented in docs/icon-catalog.md.
var validIcons = map[string]bool{
	// developer
	"code":           true,
	"terminal":       true,
	"git-branch":     true,
	"bug":            true,
	"cog":            true,
	// chat
	"message-square": true,
	"message-circle": true,
	"bot":            true,
	// data
	"bar-chart":      true,
	"database":       true,
	"trending-up":    true,
	"pie-chart":      true,
	// content
	"file-text":      true,
	"edit":           true,
	"book-open":      true,
	"image":          true,
	// search
	"search":         true,
	"globe":          true,
	"compass":        true,
	// automation
	"workflow":       true,
	"zap":            true,
	"play":           true,
	"repeat":         true,
	// media
	"video":          true,
	"music":          true,
	"mic":            true,
	// utility
	"wrench":         true,
	"shield":         true,
	"key":            true,
	"tool":           true,
	// common — cross-category generic icons
	"robot":          true,
	"sparkles":       true,
	"bolt":           true,
	"layers":         true,
	"package":        true,
	"cloud":          true,
}

// validCategories is the catalog of category IDs.
// Documented in docs/category-catalog.md.
var validCategories = map[string]bool{
	"developer":   true,
	"chat":        true,
	"data":        true,
	"content":     true,
	"search":      true,
	"automation":  true,
	"media":       true,
	"utility":     true,
}
