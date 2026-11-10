package platform

import "testing"

func TestDefaultIsFnos(t *testing.T) {
	for _, id := range []string{"", "fnos", "FNOS", "bogus"} {
		p := Parse(id)
		if p.ID != "fnos" || p.MeshOS != "fnos" || p.MeshClient != "meowx-fnos" || p.UAOS != "fnOS" || p.Docker {
			t.Fatalf("%q 应当是飞牛版：%+v", id, p)
		}
	}
}

func TestDockerEditions(t *testing.T) {
	for _, id := range []string{"docker", "synology", "qnap", "ugreen"} {
		p := Parse(id)
		if p.ID != id || !p.Docker || p.MeshClient != "meowx-docker" || p.Edition == "" {
			t.Fatalf("%q：%+v", id, p)
		}
	}
	if Parse("synology").MeshOS != "synology" || Parse("docker").MeshOS != "linux" {
		t.Fatal("报给主控的系统名不对")
	}
}
