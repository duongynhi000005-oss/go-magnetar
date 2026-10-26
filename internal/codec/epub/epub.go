package epub

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"net/url"
	"path"
	"strings"

	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/base"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/commonmark"
	"github.com/JohannesKaufmann/html-to-markdown/v2/plugin/table"
	"github.com/pkg/errors"
	"github.com/wmentor/html"
)

var ErrInvalidFileFormat = errors.New("invalid file format")

type Codec struct{}

type containerXML struct {
	Rootfiles []struct {
		FullPath string `xml:"full-path,attr"`
	} `xml:"rootfiles>rootfile"`
}

type packageOPF struct {
	Manifest struct {
		Items []struct {
			ID        string `xml:"id,attr"`
			Href      string `xml:"href,attr"`
			MediaType string `xml:"media-type,attr"`
		} `xml:"item"`
	} `xml:"manifest"`
	Spine struct {
		Itemrefs []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func (c *Codec) ReadFile(filename string) (string, error) {
	zipReader, err := zip.OpenReader(filename)
	if err != nil {
		return "", err
	}
	defer zipReader.Close()

	filesByName := make(map[string]*zip.File, len(zipReader.File))
	for _, f := range zipReader.File {
		filesByName[f.Name] = f
	}

	opfPath, err := findOPFPath(zipReader.File, filesByName)
	if err != nil {
		return "", err
	}

	opfFile, ok := filesByName[opfPath]
	if !ok {
		return "", ErrInvalidFileFormat
	}

	opfData, err := readZipEntry(opfFile)
	if err != nil {
		return "", err
	}

	var pkg packageOPF
	if err := xml.Unmarshal(opfData, &pkg); err != nil {
		return "", ErrInvalidFileFormat
	}

	opfDir := path.Dir(opfPath)
	if opfDir == "." {
		opfDir = ""
	}

	manifestHrefs := make(map[string]string, len(pkg.Manifest.Items))
	for _, item := range pkg.Manifest.Items {
		href := item.Href
		if decoded, err := url.PathUnescape(href); err == nil {
			href = decoded
		}
		if idx := strings.IndexByte(href, '#'); idx >= 0 {
			href = href[:idx]
		}
		manifestHrefs[item.ID] = href
	}

	conv := converter.NewConverter(
		converter.WithPlugins(
			base.NewBasePlugin(),
			commonmark.NewCommonmarkPlugin(),
			table.NewTablePlugin(),
		),
	)

	var chapters []string
	for _, ref := range pkg.Spine.Itemrefs {
		href, has := manifestHrefs[ref.IDRef]
		if !has || href == "" {
			continue
		}

		entryPath := href
		if opfDir != "" {
			entryPath = path.Join(opfDir, href)
		}

		chapterFile, exists := filesByName[entryPath]
		if !exists {
			continue
		}

		raw, err := readZipEntry(chapterFile)
		if err != nil {
			return "", err
		}

		text := strings.TrimSpace(xhtmlToMarkdown(conv, string(raw)))
		if text != "" {
			chapters = append(chapters, text)
		}
	}

	return strings.Join(chapters, "\n\n"), nil
}

func findOPFPath(files []*zip.File, filesByName map[string]*zip.File) (string, error) {
	if containerFile, ok := filesByName["META-INF/container.xml"]; ok {
		data, err := readZipEntry(containerFile)
		if err != nil {
			return "", err
		}
		var container containerXML
		if err := xml.Unmarshal(data, &container); err != nil {
			return "", ErrInvalidFileFormat
		}
		for _, rf := range container.Rootfiles {
			if rf.FullPath != "" {
				return rf.FullPath, nil
			}
		}
	}

	for _, f := range files {
		if strings.HasSuffix(strings.ToLower(f.Name), ".opf") {
			return f.Name, nil
		}
	}

	return "", ErrInvalidFileFormat
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	return io.ReadAll(rc)
}

func xhtmlToMarkdown(conv *converter.Converter, content string) string {
	result, err := conv.ConvertString(content)
	if err != nil {
		parser := html.New()
		parser.ParseString(content)
		return string(parser.Text())
	}
	return result
}
