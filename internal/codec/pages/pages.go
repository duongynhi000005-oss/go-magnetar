package pages

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"

	"github.com/pkg/errors"
)

var ErrInvalidFileFormat = errors.New("invalid file format")

type Codec struct{}

func (c *Codec) ReadFile(filename string) (string, error) {
	zipReader, err := zip.OpenReader(filename)
	if err != nil {
		return "", err
	}
	defer zipReader.Close()

	var previewFile *zip.File
	var indexXMLFile *zip.File

	for _, f := range zipReader.File {
		switch strings.ToLower(f.Name) {
		case "quicklook/preview.txt":
			previewFile = f
		case "index.xml", "index/document.xml":
			if indexXMLFile == nil {
				indexXMLFile = f
			}
		}
	}

	if previewFile != nil {
		text, err := readZipText(previewFile)
		if err != nil {
			return "", err
		}
		return text, nil
	}

	if indexXMLFile != nil {
		return readIndexXML(indexXMLFile)
	}

	return "", ErrInvalidFileFormat
}

func readZipText(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func readIndexXML(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	xmlData, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	var textBuffer bytes.Buffer
	decoder := xml.NewDecoder(bytes.NewReader(xmlData))

	pDepth := 0
	var paraBuffer bytes.Buffer

	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", ErrInvalidFileFormat
		}

		switch se := token.(type) {
		case xml.StartElement:
			switch se.Name.Local {
			case "p":
				pDepth++
			case "tab":
				if pDepth > 0 {
					paraBuffer.WriteByte('\t')
				}
			case "br", "lnbr", "crbr":
				if pDepth > 0 {
					paraBuffer.WriteByte('\n')
				}
			}
		case xml.CharData:
			if pDepth > 0 {
				paraBuffer.Write(se)
			}
		case xml.EndElement:
			if se.Name.Local == "p" && pDepth > 0 {
				pDepth--
				if pDepth == 0 {
					line := strings.TrimSpace(paraBuffer.String())
					paraBuffer.Reset()
					if line != "" {
						textBuffer.WriteString(line)
						textBuffer.WriteString("\n\n")
					}
				}
			}
		}
	}

	return textBuffer.String(), nil
}
