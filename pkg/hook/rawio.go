package hook

import (
	"encoding/xml"
	"fmt"
	"strings"
)

const userAliasPrefix = "ua-"

func SetRawIO(domainXML []byte, diskNames []string) ([]byte, error) {
	if len(diskNames) == 0 {
		return domainXML, nil
	}

	nameSet := make(map[string]bool, len(diskNames))
	for _, n := range diskNames {
		n = strings.TrimSpace(n)
		if n != "" {
			nameSet[userAliasPrefix+n] = true
		}
	}

	if len(nameSet) == 0 {
		return domainXML, nil
	}

	type diskAlias struct {
		Name string `xml:"name,attr"`
	}
	var parsed struct {
		XMLName xml.Name `xml:"domain"`
		Devices struct {
			Disks []struct {
				RawIO string    `xml:"rawio,attr"`
				Alias diskAlias `xml:"alias"`
			} `xml:"disk"`
		} `xml:"devices"`
	}

	if err := xml.Unmarshal(domainXML, &parsed); err != nil {
		return nil, fmt.Errorf("failed to parse domain XML: %w", err)
	}

	result := string(domainXML)

	for _, disk := range parsed.Devices.Disks {
		if !nameSet[disk.Alias.Name] {
			continue
		}

		if strings.EqualFold(strings.TrimSpace(disk.RawIO), "yes") {
			continue
		}

		result = injectRawIOAttr(result, disk.Alias.Name)
	}

	return []byte(result), nil
}

// injectRawIOAttr finds the <disk> element containing the given alias name
// and adds rawio='yes' as an attribute on the opening <disk> tag.
func injectRawIOAttr(xmlStr string, aliasName string) string {
	aliasPatterns := []string{
		fmt.Sprintf(`<alias name="%s"`, aliasName),
		fmt.Sprintf(`<alias name='%s'`, aliasName),
	}

	aliasIdx := -1
	for _, pat := range aliasPatterns {
		idx := strings.Index(xmlStr, pat)
		if idx >= 0 {
			aliasIdx = idx
			break
		}
	}

	if aliasIdx < 0 {
		return xmlStr
	}

	diskStart := strings.LastIndex(xmlStr[:aliasIdx], "<disk")
	if diskStart < 0 {
		return xmlStr
	}

	tagEnd := strings.Index(xmlStr[diskStart:], ">")
	if tagEnd < 0 {
		return xmlStr
	}

	diskTag := xmlStr[diskStart : diskStart+tagEnd]
	if strings.Contains(diskTag, "rawio=") {
		return xmlStr
	}

	insertPoint := diskStart + tagEnd
	return xmlStr[:insertPoint] + ` rawio="yes"` + xmlStr[insertPoint:]
}

func ParseDiskNames(annotation string) []string {
	if annotation == "" {
		return nil
	}
	parts := strings.Split(annotation, ",")
	var names []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			names = append(names, p)
		}
	}
	return names
}
