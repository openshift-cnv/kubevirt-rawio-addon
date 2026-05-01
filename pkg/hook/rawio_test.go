package hook

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const testDomainXML = `<domain type='kvm'>
  <name>test-vm</name>
  <memory unit='KiB'>1048576</memory>
  <devices>
    <disk type='block' device='lun'>
      <driver name='qemu' type='raw' cache='none'/>
      <source dev='/dev/sda'/>
      <target dev='sda' bus='scsi'/>
      <alias name='ua-datadisk1'/>
      <address type='drive' controller='0' bus='0' target='0' unit='0'/>
    </disk>
    <disk type='block' device='lun'>
      <driver name='qemu' type='raw' cache='none'/>
      <source dev='/dev/sdb'/>
      <target dev='sdb' bus='scsi'/>
      <alias name='ua-datadisk2'/>
      <address type='drive' controller='0' bus='0' target='0' unit='1'/>
    </disk>
    <disk type='file' device='disk'>
      <driver name='qemu' type='qcow2'/>
      <source file='/var/lib/libvirt/images/boot.qcow2'/>
      <target dev='vda' bus='virtio'/>
      <alias name='ua-rootdisk'/>
    </disk>
  </devices>
</domain>`

var _ = Describe("SetRawIO", func() {
	It("should set rawio on a single matching disk", func() {
		result, err := SetRawIO([]byte(testDomainXML), []string{"datadisk1"})
		Expect(err).NotTo(HaveOccurred())

		Expect(containsRawIOForDisk(string(result), "ua-datadisk1")).To(BeTrue())
		Expect(containsRawIOForDisk(string(result), "ua-datadisk2")).To(BeFalse())
	})

	It("should set rawio on multiple matching disks", func() {
		result, err := SetRawIO([]byte(testDomainXML), []string{"datadisk1", "datadisk2"})
		Expect(err).NotTo(HaveOccurred())

		Expect(containsRawIOForDisk(string(result), "ua-datadisk1")).To(BeTrue())
		Expect(containsRawIOForDisk(string(result), "ua-datadisk2")).To(BeTrue())
		Expect(containsRawIOForDisk(string(result), "ua-rootdisk")).To(BeFalse())
	})

	It("should not modify XML when no disks match", func() {
		result, err := SetRawIO([]byte(testDomainXML), []string{"nonexistent"})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result)).NotTo(ContainSubstring("rawio="))
	})

	It("should return XML unchanged when no disk names provided", func() {
		result, err := SetRawIO([]byte(testDomainXML), []string{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(result)).To(Equal(testDomainXML))
	})

	It("should not duplicate rawio when already present", func() {
		xmlWithRawIO := `<domain type='kvm'>
  <devices>
    <disk type='block' device='lun' rawio='yes'>
      <driver name='qemu' type='raw'/>
      <source dev='/dev/sda'/>
      <target dev='sda' bus='scsi'/>
      <alias name='ua-datadisk1'/>
    </disk>
  </devices>
</domain>`

		result, err := SetRawIO([]byte(xmlWithRawIO), []string{"datadisk1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.Count(string(result), "rawio=")).To(Equal(1))
	})
})

var _ = Describe("ParseDiskNames", func() {
	DescribeTable("should parse comma-separated disk names",
		func(input string, expected []string) {
			Expect(ParseDiskNames(input)).To(Equal(expected))
		},
		Entry("single disk", "datadisk1", []string{"datadisk1"}),
		Entry("multiple disks", "datadisk1,datadisk2", []string{"datadisk1", "datadisk2"}),
		Entry("with spaces", " datadisk1 , datadisk2 ", []string{"datadisk1", "datadisk2"}),
		Entry("empty string", "", nil),
		Entry("only commas", ",,,", nil),
	)
})

func containsRawIOForDisk(xmlStr, aliasName string) bool {
	aliasPattern := `<alias name='` + aliasName + `'`
	altPattern := `<alias name="` + aliasName + `"`

	aliasIdx := strings.Index(xmlStr, aliasPattern)
	if aliasIdx < 0 {
		aliasIdx = strings.Index(xmlStr, altPattern)
	}
	if aliasIdx < 0 {
		return false
	}

	diskStart := strings.LastIndex(xmlStr[:aliasIdx], "<disk")
	if diskStart < 0 {
		return false
	}
	tagEnd := strings.Index(xmlStr[diskStart:], ">")
	if tagEnd < 0 {
		return false
	}
	diskTag := xmlStr[diskStart : diskStart+tagEnd]
	return strings.Contains(diskTag, `rawio="yes"`)
}
