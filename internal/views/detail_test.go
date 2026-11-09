package views

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

func TestBuildInfo_SkipsEmptyValues(t *testing.T) {
	text, links := buildInfo([]infoField{
		{K: "ID", V: "x-1"},
		{K: "Name", V: ""},
		{K: "State", V: "ok"},
	})
	assert.Equal(t, "ID               x-1\nState            ok\n", text)
	assert.Empty(t, links)
}

func TestBuildInfo_LinksCountPrintedLinesOnly(t *testing.T) {
	params := map[string]string{"focus": "vpc-1"}
	text, links := buildInfo([]infoField{
		{K: "ID", V: "x-1"},
		{K: "Skipped", V: ""},
		{K: "Skipped link", V: "", ViewID: "subnet_list"},
		{K: "VPC", V: "vpc-1", ViewID: "vpc_list", Params: params},
	})
	assert.Equal(t, "ID               x-1\nVPC              vpc-1\n", text)
	assert.Equal(t, []msg.TabLink{{Line: 1, ViewID: "vpc_list", Params: params}}, links)
}

func TestBuildInfo_Empty(t *testing.T) {
	text, links := buildInfo(nil)
	assert.Empty(t, text)
	assert.Empty(t, links)
}

func TestYesNo(t *testing.T) {
	assert.Equal(t, "Yes", yesNo(true))
	assert.Equal(t, "No", yesNo(false))
}

func TestBuildTagsContent_SortedByKey(t *testing.T) {
	got := buildTagsContent(map[string]string{"env": "prod", "Name": "web"})
	assert.Equal(t, "Name                     web\nenv                      prod\n", got)
	assert.Empty(t, buildTagsContent(nil))
}

func TestRootDevice(t *testing.T) {
	assert.Empty(t, rootDevice(&aws.InstanceDetail{}))
	assert.Equal(t, "ebs (/dev/xvda)", rootDevice(&aws.InstanceDetail{RootDeviceType: "ebs", RootDeviceName: "/dev/xvda"}))
	assert.Equal(t, " (/dev/xvda)", rootDevice(&aws.InstanceDetail{RootDeviceName: "/dev/xvda"}))
}

// An instance with no root device data prints no Root Device line, and the
// links of VPC, Subnet and AMI keep their lines.
func TestEC2Info_NoRootDeviceLine(t *testing.T) {
	text, links := buildEC2InfoContentWithLinks(&aws.InstanceDetail{
		InstanceID: "i-1",
		VpcID:      "vpc-1",
		SubnetID:   "subnet-1",
		AMI:        "ami-1",
	})
	assert.NotContains(t, text, "Root Device")
	assert.Equal(t, "Instance ID      i-1\nVPC              vpc-1\nSubnet           subnet-1\nAMI              ami-1\n", text)
	assert.Equal(t, []msg.TabLink{
		{Line: 1, ViewID: "vpc_list", Params: map[string]string{"focus": "vpc-1"}},
		{Line: 2, ViewID: "subnet_list", Params: map[string]string{"focus": "subnet-1"}},
		{Line: 3, ViewID: "ami_list", Params: map[string]string{"focus": "ami-1"}},
	}, links)
}
