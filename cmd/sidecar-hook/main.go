package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/pflag"

	"github.com/openshift-cnv/rawio-addon/pkg/hook"
)

const annotationRawIO = "kubevirt.io/rawioSupport"

func onDefineDomain(vmiJSON, domainXML []byte) (string, error) {
	var vmi struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(vmiJSON, &vmi); err != nil {
		return "", fmt.Errorf("failed to unmarshal VMI: %w", err)
	}

	rawioAnnotation, ok := vmi.Metadata.Annotations[annotationRawIO]
	if !ok || rawioAnnotation == "" {
		return string(domainXML), nil
	}

	diskNames := hook.ParseDiskNames(rawioAnnotation)
	log.Printf("setting rawio for disks: %s", strings.Join(diskNames, ", "))

	modifiedXML, err := hook.SetRawIO(domainXML, diskNames)
	if err != nil {
		return "", fmt.Errorf("failed to set rawio: %w", err)
	}

	return string(modifiedXML), nil
}

func main() {
	var vmiJSON, domainXML string
	pflag.StringVar(&vmiJSON, "vmi", "", "VMI spec in JSON format")
	pflag.StringVar(&domainXML, "domain", "", "Domain spec in XML format")
	pflag.Parse()

	logger := log.New(os.Stderr, "rawio-hook ", log.Ldate)
	if vmiJSON == "" || domainXML == "" {
		logger.Printf("Bad input vmi=%d, domain=%d", len(vmiJSON), len(domainXML))
		os.Exit(1)
	}

	result, err := onDefineDomain([]byte(vmiJSON), []byte(domainXML))
	if err != nil {
		logger.Printf("onDefineDomain failed: %s", err)
		panic(err)
	}
	fmt.Println(result)
}
