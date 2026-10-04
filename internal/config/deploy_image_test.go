package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 三份部署示例里有两份要镜像（compose、k8s），而在此之前它们各自写着
// 本地构建出来的名字 —— 其中 k8s 那个 `image: javdb-rss:latest` 其实**无处可拉**。
// 现在两份都指向 GHCR 上已发布的镜像（见 docs/adr/0001-release-images-to-ghcr.md），
// 本测试守着三件事：
//
//  1. 两份钉的是**同一个**全量精度 tag（一份改了另一份没改＝有人 deploy 到旧版本）；
//  2. 那份 tag 不是浮动形状（latest / 1 / 1.0）—— ADR 明确不推浮动 tag，
//     而浮动 tag 会让 `docker compose pull` 静默换版本；
//  3. systemd 那份仍然跑**二进制**（它没有、也不该有镜像坐标）,
//     compose 那份是真的 `image:` 而不是又回到 `build:`。
//
// 为什么值得写：这两份文件漂移的失败方式是**静默的** —— 配置照样解析、
// 容器照样起来，只是跑的不是你以为的那份代码。这与三份配置示例的键集合
// （examples_sync_test.go）是同一类问题，所以放在同一个包里。

const (
	// 镜像坐标。owner 全小写是 ghcr 的硬要求（见 release.yml）。
	deployImageRepo = "ghcr.io/2017fighting/javdb-rss"
	// 全量精度 tag：X.Y.Z，可选预发布后缀（v1.2.3-rc1 → 1.2.3-rc1）。
	// 刻意不匹配 `1`、`1.0`、`latest`。
	pinnedTagPattern = `^` + deployImageRepo + `:\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`
)

// readDeployFile 读 deploy/ 下的一份文件（报错里带上文件名，说清是哪一处）。
func readDeployFile(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "deploy", name))
	if err != nil {
		t.Fatalf("读取 deploy/%s: %v", name, err)
	}
	return raw
}

// composeImage 取 compose 服务 javdb-rss 的 image:，同时回报它是否还留着
// 生效的 build: 段。
func composeImage(t *testing.T, raw []byte) (image string, hasBuild bool) {
	t.Helper()
	var doc struct {
		Services map[string]struct {
			Image string `yaml:"image"`
			Build any    `yaml:"build"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析 deploy/docker-compose.yml: %v", err)
	}
	svc, ok := doc.Services["javdb-rss"]
	if !ok {
		t.Fatal("deploy/docker-compose.yml 里没有 javdb-rss 服务")
	}
	return svc.Image, svc.Build != nil
}

// k8sDeploymentImage 从多文档清单里取 Deployment javdb-rss 的容器镜像。
func k8sDeploymentImage(t *testing.T, raw []byte) string {
	t.Helper()
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Name  string `yaml:"name"`
							Image string `yaml:"image"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		err := dec.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("解析 deploy/k8s.yaml: %v", err)
		}
		if doc.Kind != "Deployment" || doc.Metadata.Name != "javdb-rss" {
			continue
		}
		for _, c := range doc.Spec.Template.Spec.Containers {
			if c.Name == "javdb-rss" {
				return c.Image
			}
		}
		t.Fatal("deploy/k8s.yaml 的 Deployment 里没有名为 javdb-rss 的容器")
	}
	t.Fatal("deploy/k8s.yaml 里找不到 Deployment javdb-rss")
	return ""
}

// deployImageProblems 是判定本体：给定两份容器部署的镜像坐标（compose 那份
// 还带上它是否留着生效的 build:），返回问题清单，空表示没问题。
//
// 抽成纯函数只为一件事：让一个元测试能拿**造出来的**漂移输入证明它不是空转
// （与 examples_sync_test.go 里的 TestKeyCheckCatchesDrift 同一动机）。
func deployImageProblems(composeImage, k8sImage string, composeHasBuild bool) []string {
	var problems []string

	if composeImage == "" {
		problems = append(problems, "deploy/docker-compose.yml 没有 image: —— 部署应当拉已发布的镜像，不是本地构建")
	}
	if composeHasBuild {
		problems = append(problems, "deploy/docker-compose.yml 仍有生效的 build: 段 —— "+
			"它会让我们构建本地镜像而不是拉已发布的那份（想自建的人用注释里的写法）")
	}
	if composeImage != k8sImage {
		problems = append(problems, fmt.Sprintf("两份部署钉的不是同一个镜像：\n  docker-compose.yml: %s\n  k8s.yaml:           %s",
			composeImage, k8sImage))
	}

	re := regexp.MustCompile(pinnedTagPattern)
	for _, got := range []struct{ where, image string }{
		{"deploy/docker-compose.yml", composeImage},
		{"deploy/k8s.yaml", k8sImage},
	} {
		if !re.MatchString(got.image) {
			problems = append(problems, fmt.Sprintf("%s 的镜像坐标 %q 不是 %s 的全量精度 tag —— "+
				"浮动 tag（latest/1/1.0）会让 pull 静默换版本（见 ADR 0001）",
				got.where, got.image, deployImageRepo+":X.Y.Z"))
		}
	}
	return problems
}

// TestDeployManifestsPinSamePublishedImage 是主检查：两份容器部署钉的是
// 同一个已发布的全量精度 tag。
func TestDeployManifestsPinSamePublishedImage(t *testing.T) {
	composeImg, hasBuild := composeImage(t, readDeployFile(t, "docker-compose.yml"))
	k8sImg := k8sDeploymentImage(t, readDeployFile(t, "k8s.yaml"))

	for _, p := range deployImageProblems(composeImg, k8sImg, hasBuild) {
		t.Error(p)
	}
}

// TestDeployImageCheckCatchesDrift 是元测试：拿造出来的漂移输入证明上面的
// 检查会失败 —— 否则「它绿了」可能只是因为它什么都没看。
func TestDeployImageCheckCatchesDrift(t *testing.T) {
	const pinned = deployImageRepo + ":1.0.0"

	cases := []struct {
		name            string
		compose, k8s    string
		composeHasBuild bool
		wantProblem     bool
	}{
		{"两份一致且已钉", pinned, pinned, false, false},
		{"k8s 漂到 latest（本票之前的形状）", pinned, deployImageRepo + ":latest", false, true},
		{"k8s 漂到浮动小版本", pinned, deployImageRepo + ":1.0", false, true},
		{"compose 退回本地构建", pinned, pinned, true, true},
		{"compose 没有 image", "", deployImageRepo + ":latest", false, true},
		{"两处都是本地镜像名", "javdb-rss:latest", "javdb-rss:latest", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deployImageProblems(tc.compose, tc.k8s, tc.composeHasBuild)
			if tc.wantProblem == (len(got) == 0) {
				t.Fatalf("问题清单 = %v, wantProblem = %v", got, tc.wantProblem)
			}
		})
	}
}

// TestSystemdUnitRunsBinaryNotImage 记下另一半事实：systemd 部署跑的是二进制。
// 它不需要镜像、也不需要 Docker，因此升级方式是换那个文件。
func TestSystemdUnitRunsBinaryNotImage(t *testing.T) {
	unit := string(readDeployFile(t, "javdb-rss.service"))

	if !strings.Contains(unit, "ExecStart=/usr/local/bin/javdb-rss") {
		t.Error("deploy/javdb-rss.service 的 ExecStart 不再指向 /usr/local/bin/javdb-rss —— " +
			"这份部署跑的是二进制，改动它之前先想清楚 README 部署表该怎么写")
	}
	if strings.Contains(unit, "ghcr.io/") {
		t.Error("deploy/javdb-rss.service 里出现了镜像坐标 —— systemd 这份跑的是二进制，不拉镜像")
	}
}
