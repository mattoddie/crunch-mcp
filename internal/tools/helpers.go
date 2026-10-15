package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mattoddie/crunch-mcp/internal/crunch"
)

// ---------------------------------------------------------------- paging

// PageArgs are the paging arguments of list tools over Spring-style paged endpoints.
type PageArgs struct {
	Page     *int   `json:"page,omitempty" jsonschema:"Page to fetch, starting at 0 (default 0)"`
	Size     *int   `json:"size,omitempty" jsonschema:"Results per page, up to 200 (default 50)"`
	Sort     string `json:"sort,omitempty" jsonschema:"Sort order as field,asc or field,desc, for example postingDate,desc"`
	All      bool   `json:"all,omitempty" jsonschema:"Fetch every page (up to max_items) instead of a single page"`
	MaxItems *int   `json:"max_items,omitempty" jsonschema:"With all=true, stop after this many results (default 1000)"`
}

// fetchPaged fetches one page, or every page with All, and maps the items.
func fetchPaged(ctx context.Context, c *crunch.Client, path string, q crunch.Query, a PageArgs, defaultSort string, mapFn func(Obj) Obj) (Obj, error) {
	if mapFn == nil {
		mapFn = func(o Obj) Obj { return o }
	}
	if q == nil {
		q = crunch.Query{}
	}
	sort := a.Sort
	if sort == "" {
		sort = defaultSort
	}
	q["sort"] = sort
	mapAll := func(items []Obj) []Obj {
		out := make([]Obj, len(items))
		for i, it := range items {
			out[i] = mapFn(it)
		}
		return out
	}
	page := 0
	if a.Page != nil {
		page = max(*a.Page, 0)
	}
	if !a.All {
		size := 50
		if a.Size != nil {
			size = min(max(*a.Size, 1), 200)
		}
		q["page"], q["size"] = page, size
		var res Obj
		if err := c.Get(ctx, path, q, &res); err != nil {
			return nil, err
		}
		items := crunch.List(res, "content")
		out := Obj{"page": page, "returned": len(items), "items": mapAll(items)}
		if n := crunch.ID(res, "totalElements"); n != nil {
			out["total"] = n
		}
		if n := crunch.ID(res, "totalPages"); n != nil {
			out["pages"] = n
		}
		if last, ok := res["last"].(bool); ok && !last {
			out["next_page"] = page + 1
		}
		return out, nil
	}
	maxItems := 1000
	if a.MaxItems != nil && *a.MaxItems > 0 {
		maxItems = *a.MaxItems
	}
	var items []Obj
	var total any
	last := false
	for !last && len(items) < maxItems {
		q["page"], q["size"] = page, 200
		var res Obj
		if err := c.Get(ctx, path, q, &res); err != nil {
			return nil, err
		}
		content := crunch.List(res, "content")
		items = append(items, content...)
		if n := crunch.ID(res, "totalElements"); n != nil {
			total = n
		}
		l, ok := res["last"].(bool)
		last = !ok || l || len(content) == 0
		page++
	}
	if total == nil {
		total = len(items)
	}
	out := Obj{"total": total}
	if len(items) > maxItems || !last {
		out["truncated"] = true
		out["note"] = fmt.Sprintf("Stopped at max_items=%d; raise it or narrow the filters to see the rest.", maxItems)
	}
	if len(items) > maxItems {
		items = items[:maxItems]
	}
	out["returned"] = len(items)
	out["items"] = mapAll(items)
	return out, nil
}

// filterAndLimit applies a case-insensitive substring search over some fields, then a limit.
func filterAndLimit(items []Obj, search string, limit *int, fields func(Obj) []string) Obj {
	out := items
	if search != "" {
		q := strings.ToLower(search)
		out = nil
		for _, it := range items {
			for _, f := range fields(it) {
				if f != "" && strings.Contains(strings.ToLower(f), q) {
					out = append(out, it)
					break
				}
			}
		}
	}
	total := len(out)
	if limit != nil && *limit >= 0 && *limit < len(out) {
		out = out[:*limit]
	}
	if out == nil {
		out = []Obj{}
	}
	return Obj{"total": total, "returned": len(out), "items": out}
}

func mapItems(items []Obj, f func(Obj) Obj) []Obj {
	out := make([]Obj, len(items))
	for i, it := range items {
		out[i] = f(it)
	}
	return out
}

// ---------------------------------------------------------------- objects

// deepMerge applies a partial update to an object fetched from the API:
// nested objects merge, while arrays and plain values replace what was there.
func deepMerge(base, patch Obj) Obj {
	out := Obj{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range patch {
		if pm, ok := v.(Obj); ok {
			if bm, ok := out[k].(Obj); ok {
				out[k] = deepMerge(bm, pm)
				continue
			}
		}
		out[k] = v
	}
	return out
}

// set copies only non-empty values into an object (zero strings and nil pointers are skipped).
func set(o Obj, kv ...any) Obj {
	for i := 0; i+1 < len(kv); i += 2 {
		k := kv[i].(string)
		switch v := kv[i+1].(type) {
		case nil:
		case string:
			if v != "" {
				o[k] = v
			}
		case Money:
			if v != "" {
				o[k] = v
			}
		case *int64:
			if v != nil {
				o[k] = *v
			}
		case *int:
			if v != nil {
				o[k] = *v
			}
		case *bool:
			if v != nil {
				o[k] = *v
			}
		case Obj:
			if v != nil {
				o[k] = v
			}
		case []Obj:
			if v != nil {
				o[k] = v
			}
		default:
			o[k] = v
		}
	}
	return o
}

func ref(id *int64) Obj {
	if id == nil {
		return nil
	}
	return Obj{"id": *id}
}

func merge(base Obj, extra map[string]any) Obj {
	for k, v := range extra {
		base[k] = v
	}
	return base
}

func path(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// ---------------------------------------------------------------- files

var mimeTypes = map[string]string{
	".pdf": "application/pdf", ".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".heic": "image/heic", ".tif": "image/tiff", ".tiff": "image/tiff", ".csv": "text/csv", ".txt": "text/plain",
	".doc": "application/msword", ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls": "application/vnd.ms-excel", ".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
}

func mimeTypeFor(name string) string {
	if t, ok := mimeTypes[strings.ToLower(filepath.Ext(name))]; ok {
		return t
	}
	if t := mime.TypeByExtension(filepath.Ext(name)); t != "" {
		t, _, _ = strings.Cut(t, ";")
		return t
	}
	return "application/octet-stream"
}

const (
	maxUploadBytes = 10 << 20
	maxEmbedBytes  = 8 << 20 // larger downloads are saved or described, not embedded in the result
)

// Attachment is a file to upload.
type Attachment struct {
	FileName      string `json:"file_name" jsonschema:"File name including its extension, for example receipt.jpg"`
	ContentBase64 string `json:"content_base64,omitempty" jsonschema:"The file as base64, or as a data URI (data:image/jpeg;base64,...)"`
	FilePath      string `json:"file_path,omitempty" jsonschema:"Instead of content_base64: path to the file on the machine running this server"`
	MimeType      string `json:"mime_type,omitempty" jsonschema:"MIME type, for example image/jpeg (guessed from the file name when omitted)"`
}

var (
	dataURIRe = regexp.MustCompile(`(?s)^data:([^;,]+)?(?:;[^,]*)?,(.*)$`)
	spaceRe   = regexp.MustCompile(`\s+`)
)

// dataURI builds the data URI Crunch expects in fileContents. Raw base64
// without the data: prefix is accepted by the API but saves an empty file.
func (a Attachment) dataURI() (string, error) {
	mimeType := a.MimeType
	var b64 string
	switch {
	case a.FilePath != "":
		st, err := os.Stat(a.FilePath)
		if err != nil {
			return "", err
		}
		if st.Size() > maxUploadBytes {
			return "", fmt.Errorf("%s is larger than 10 MB", a.FilePath)
		}
		data, err := os.ReadFile(a.FilePath)
		if err != nil {
			return "", err
		}
		b64 = base64.StdEncoding.EncodeToString(data)
	case a.ContentBase64 != "":
		b64 = a.ContentBase64
		if m := dataURIRe.FindStringSubmatch(b64); m != nil {
			if mimeType == "" {
				mimeType = m[1]
			}
			b64 = m[2]
		}
		b64 = spaceRe.ReplaceAllString(b64, "")
		if _, err := base64.StdEncoding.DecodeString(b64); err != nil {
			if _, err2 := base64.RawStdEncoding.DecodeString(b64); err2 != nil {
				return "", fmt.Errorf("the content of %s is not valid base64", a.FileName)
			}
		}
		if len(b64)*3/4 > maxUploadBytes {
			return "", fmt.Errorf("%s is larger than 10 MB", a.FileName)
		}
	default:
		return "", fmt.Errorf("give content_base64 or file_path for %s", a.FileName)
	}
	if mimeType == "" {
		mimeType = mimeTypeFor(a.FileName)
	}
	return "data:" + mimeType + ";base64," + b64, nil
}

// attachmentsBody numbers attachments from firstID, as Crunch expects (0, 1, 2...).
func attachmentsBody(files []Attachment, firstID int64) ([]Obj, error) {
	out := make([]Obj, 0, len(files))
	for i, f := range files {
		uri, err := f.dataURI()
		if err != nil {
			return nil, err
		}
		out = append(out, Obj{"id": firstID + int64(i), "fileName": f.FileName, "fileContents": uri})
	}
	return out, nil
}

// saveFile writes a downloaded file to saveTo (a directory or a file path) without overwriting anything.
func saveFile(saveTo, name string, data []byte) (string, error) {
	target := saveTo
	if st, err := os.Stat(saveTo); err == nil && st.IsDir() {
		target = filepath.Join(saveTo, filepath.Base(name))
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("%s already exists; choose another save_to", target)
		}
		return "", err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	return target, f.Close()
}

// fileResult returns a downloaded file: images inline, other files (PDFs) as an embedded resource.
func fileResult(f *crunch.File, fallbackName, uri, saveTo string) (Result, error) {
	name := f.Filename
	if name == "" {
		name = fallbackName
	}
	mimeType := f.ContentType
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = mimeTypeFor(name)
	}
	meta := Obj{"file_name": name, "mime_type": mimeType, "bytes": len(f.Data)}
	if saveTo != "" {
		saved, err := saveFile(saveTo, name, f.Data)
		if err != nil {
			return Result{}, err
		}
		meta["saved_to"] = saved
	}
	var content []mcp.Content
	switch {
	case len(f.Data) > maxEmbedBytes:
		note := fmt.Sprintf("Too large to return inline (over %d MB)", maxEmbedBytes>>20)
		if saveTo == "" {
			note += "; use save_to to save it"
		}
		meta["note"] = note + "."
	case strings.HasPrefix(mimeType, "image/"):
		content = append(content, &mcp.ImageContent{Data: f.Data, MIMEType: mimeType})
	default:
		content = append(content, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: uri, MIMEType: mimeType, Blob: f.Data}})
	}
	text := textResult(meta)
	text.Content = append(text.Content, content...)
	return Result{text}, nil
}
