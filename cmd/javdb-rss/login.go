package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/config"
)

// runLogin 是 `javdb-rss login` 子命令。
//
// 它做四件事：拿凭据 → 登录 → 写 token 文件 → 验证 token 真的能用。
//
// # ⚠️ 为什么必须在登录前就把后果说清楚
//
// 用户实测报告：同一账号只能在一个地方登录，**新登录会挤掉之前那个**。
// 也就是说这个命令一跑，用户手机上的 App 就被踢下线了。
//
// 这个后果无法在事后补救，也不该藏在文档里 —— 所以它在命令输出里最先出现。
func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	configPath := fs.String("config", "config.yaml", "配置文件路径")
	force := fs.Bool("force", false, "即使已配置 JAVDB_TOKEN 也照常写文件")
	if err := fs.Parse(args); err != nil {
		return err
	}

	holder, err := config.NewHolder(*configPath)
	if err != nil {
		return err
	}
	ac := holder.Current().AppAPI

	fmt.Println()
	fmt.Println("⚠️  这一步会把你手机 App 上的登录挤下线。")
	fmt.Println("    如果你正打算用手机看，先看完再回来跑这个命令。")
	fmt.Println()

	// 环境变量优先于文件。若它已设，写文件是白费力气 —— 下次启动读到的还是 env。
	if v := strings.TrimSpace(os.Getenv(config.EnvToken)); v != "" && !*force {
		fmt.Printf("提示：%s 已经设置了（优先于文件），所以本次不会写 token 文件。\n", config.EnvToken)
		fmt.Println("      想改用文件管理 token，请先 unset 它，或加 -force 强制写。")
		fmt.Println()
	}

	username, password, fromEnv := credentials()
	if fromEnv {
		fmt.Printf("使用环境变量里的凭据（用户名 %s）。\n\n", username)
	}

	// 与库里其它地方一致：每次请求重建客户端，好让配置可热重载。
	identity := appapi.DefaultIdentity()
	if ac.DeviceUUID != "" {
		identity.DeviceUUID = ac.DeviceUUID
	}
	cl := &appapi.Client{
		Host:     ac.Host,
		Identity: identity,
		Signer:   appapi.NewSigner(),
		Lang:     ac.Lang,
	}

	ctx := context.Background()

	fmt.Printf("正在登录 %s …\n", ac.Host)
	token, err := cl.Login(ctx, username, password)
	if err != nil {
		return fmt.Errorf("登录失败：%w\n\n（提示：密码错误时上游返回的是 IncorrentUsernameOrPassword —— 那是它自己的拼写）", err)
	}
	fmt.Println("✓ 登录成功")

	if err := config.SaveToken(ac.TokenFile, token); err != nil {
		return fmt.Errorf("保存 token 失败：%w", err)
	}
	abs := ac.TokenFile
	if a, err := os.Getwd(); err == nil {
		abs = strings.TrimPrefix(ac.TokenFile, a+"/")
	}
	fmt.Printf("✓ 已写入 %s（权限 0600）\n", abs)

	// 验证：拿新 token 去打一个**真的需要凭据**的端点。
	//
	// 只用 Login 成功来判定是不够的 —— 那只证明上游给了我们一串字符，
	// 不证明这串字符在后续请求里被承认。而这里失败的话，
	// 用户会在几天后才发现「怎么 /collected 一直 503」。
	cl.Token = token
	actresses, err := cl.CollectedActresses(ctx)
	if err != nil {
		return fmt.Errorf(
			"token 已写入，但**验证失败**：%w\n\n"+
				"这可能意味着上游改了鉴权方式。请把这条错误报出来，不要以为只是网络问题", err)
	}
	fmt.Printf("✓ 验证通过：读到 %d 位收藏女优\n", len(actresses))
	for i, a := range actresses {
		if i >= 5 {
			fmt.Printf("    … 另有 %d 位\n", len(actresses)-5)
			break
		}
		name := a.Name
		if name == "" {
			name = "(无名)"
		}
		fmt.Printf("    %-8s %s\n", a.ID, name)
	}
	if len(actresses) == 0 {
		fmt.Println("    （0 位 —— 如果你在 App 里确实收藏过女优，这值得查一下）")
	}

	fmt.Println()
	base := strings.TrimRight(holder.Current().Feed.BaseURL, "/")
	fmt.Println("现在可以在 feed 里用了：")
	fmt.Printf("    %s/rss/actress/<上面的 id>.xml\n", base)
	fmt.Printf("    %s/collected   可以随时查这份列表\n", base)
	fmt.Println()
	fmt.Println("⚠️  token 失效时（README 里有说明）需要重跑本命令，而那会再次踢掉手机。")
	if _, _, hasCreds := config.LoadCredentials(); !hasCreds {
		fmt.Printf("    想让它自动续期，可以设 %s 与 %s —— 但每次自动续期同样会踢掉手机。\n",
			config.EnvUsername, config.EnvPassword)
	}
	fmt.Println()
	return nil
}

// credentials 拿登录凭据：环境变量优先，否则交互式询问。
//
// 环境变量优先是为了可脚本化（也正是用户选的 .env 用法）。
// 交互式路径用 term.ReadPassword，让密码不被回显 —— 不隐藏密码的提示
// 会把密码留在终端回滚缓冲与肩膀偷看的射程内。
func credentials() (username, password string, fromEnv bool) {
	if u, p, ok := config.LoadCredentials(); ok {
		return u, p, true
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Print("用户名：")
	u, _ := reader.ReadString('\n')
	username = strings.TrimSpace(u)

	fmt.Print("密码（输入时不显示）：")
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err == nil {
			password = string(b)
		}
	} else {
		// 非终端（例如把密码管进来）：直接读一行。
		p, _ := reader.ReadString('\n')
		password = strings.TrimSpace(p)
	}
	return username, password, false
}
