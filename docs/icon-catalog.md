# Icon catalog

`meta.yaml`'s `icon` field takes a **semantic identifier** from this list. The marketplace-api serves these identifiers as plain strings; the frontend maps them to its own icon library (e.g. lucide-react, heroicons).

| Category | Identifier | Suggested icon (lucide) |
|----------|-----------|-------------------------|
| **developer** | `code` | `<Code />` |
|  | `terminal` | `<Terminal />` |
|  | `git-branch` | `<GitBranch />` |
|  | `bug` | `<Bug />` |
|  | `cog` | `<Cog />` |
| **chat** | `message-square` | `<MessageSquare />` |
|  | `message-circle` | `<MessageCircle />` |
|  | `bot` | `<Bot />` |
| **data** | `bar-chart` | `<BarChart />` |
|  | `database` | `<Database />` |
|  | `trending-up` | `<TrendingUp />` |
|  | `pie-chart` | `<PieChart />` |
| **content** | `file-text` | `<FileText />` |
|  | `edit` | `<Edit />` |
|  | `book-open` | `<BookOpen />` |
|  | `image` | `<Image />` |
| **search** | `search` | `<Search />` |
|  | `globe` | `<Globe />` |
|  | `compass` | `<Compass />` |
| **automation** | `workflow` | `<Workflow />` |
|  | `zap` | `<Zap />` |
|  | `play` | `<Play />` |
|  | `repeat` | `<Repeat />` |
| **media** | `video` | `<Video />` |
|  | `music` | `<Music />` |
|  | `mic` | `<Mic />` |
| **utility** | `wrench` | `<Wrench />` |
|  | `shield` | `<Shield />` |
|  | `key` | `<Key />` |
|  | `tool` | `<Tool />` |
| **common** | `robot` | generic AI / agent |
|  | `sparkles` | AI / smart features |
|  | `bolt` | fast / high-performance |
|  | `layers` | multi-layer / composite |
|  | `package` | package / bundle |
|  | `cloud` | cloud / remote |

## Adding new icons

To add a new identifier:

1. Add the string to `validIcons` in `internal/manifest/catalogs.go`.
2. Add a row to the table above with a recommended lucide / heroicons mapping.
3. Update the frontend's `iconMap` to include the new identifier.

Don't invent a new icon system — extend the catalog, keep the simple string contract.