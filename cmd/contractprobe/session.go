package main

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/2017fighting/javdb_rss/internal/appapi"
	"github.com/2017fighting/javdb_rss/internal/config"
)

// session 回答两条**只能用真实账号测**的断言：
//
//	① `device_uuid` 是否被用作设备指纹
//	② 单会话「挤掉」的具体语义 —— 立即失效，还是有一段宽限期
//
// # ⚠️ 为什么它必须先把后果打在屏幕上
//
// 单会话账号：**这个命令一跑，用户手机上的 App 就被踢下线**。
// 这与 `javdb-rss login` 的副作用完全一样，因此提示也照抄那一条的措辞 ——
// 用户没有别的途径能提前知道这件事。
//
// # 两条断言各怎么测
//
// ① 指纹：同一个 token，配**三个不同的 device_uuid** 各打一次需要凭据的端点。
//
//	如果服务端把 token 绑在某个设备标识上，后两次就应当被拒。
//	这条能给出一个可操作的结论：换 device_uuid 会不会让**已发的 token 失效**。
//	（它答不了的是「上游是否在内部统计 device_uuid」—— 那是服务端内部状态，
//	从外部不可观测，代价见 notes。）
//
// ② 挤掉：登录取 token1 → 验证 → 再登录取 token2 → 从那一刻起按递增的间隔
//
//	反复用 token1 打同一个端点，记录**首次被拒**距 token2 登录多久。
//	0 秒即失效就是「立即」，持续可用一段时间就是「有宽限期」。
//
// 两次登录刻意用**不同的 device_uuid**：这样「同一账号不同设备再登录」也算测到，
// 否则无法把「挤掉是因为同一设备重复登录」与「挤掉是因为单会话」分开。
func (p *probe) session(wait time.Duration) error {
	fmt.Println()
	fmt.Println("⚠️  这个实验会登录两次 —— 每次登录都会把你手机 App 上的会话挤下线。")
	fmt.Println("    跑完后要继续用服务，请重跑 `javdb-rss login`。")
	fmt.Println("    （本探针**不会**写你的 token 文件：探针不应改动被观测的系统的状态。）")
	fmt.Println()

	username, password, ok := config.LoadCredentials()
	if !ok {
		return fmt.Errorf("需要凭据：设 %s 与 %s（只设一个不算）",
			config.EnvUsername, config.EnvPassword)
	}

	// ---------- ① device_uuid ----------

	fmt.Println("=== ① device_uuid 是否被用作设备指纹 ===")
	first, err := p.login(username, password, deviceA)
	if err != nil {
		return err
	}
	fmt.Printf("已登录（device_uuid=%s）\n", deviceA)
	if err := p.checkAuth("token1 + 原 device_uuid", first, deviceA); err != nil {
		return err
	}
	// 换成两个不同的值：一个是另一个固定值，一个是随机值。
	// 只试一个值的话，无法区分「这个值恰好被接受」与「device_uuid 根本不参与判定」。
	_ = p.checkAuth("token1 + 另一个固定 uuid", first, deviceB)
	_ = p.checkAuth("token1 + 随机 uuid", first, newUUID())

	// ---------- ② 单会话挤掉 ----------

	fmt.Println("\n=== ② 单会话「挤掉」的语义 ===")
	fmt.Println("做法：让第二次登录在后台进行，同时在前台**持续用 token1 打端点**，")
	fmt.Println("      直到它被拒。这样测到的是「失效发生在登录的哪个阶段」，")
	fmt.Println("      而不是「我们什么时候才有空去看一眼」。")
	fmt.Println()

	type loginResult struct {
		token string
		at    time.Duration
		err   error
	}
	tSwitch := time.Now()
	loginCh := make(chan loginResult, 1)
	go func() {
		// 刻意用**不同**的 device_uuid：这样「同一账号换个设备再登录」
		// 也一并测到了 —— 否则无法把「挤掉」与「同一设备重复登录」分开。
		tok, err := p.login(username, password, deviceB)
		loginCh <- loginResult{token: tok, at: time.Since(tSwitch), err: err}
	}()

	type poll struct {
		// at 是请求**发出**的时刻（相对 tSwitch）。
		//
		// 必须区分发出与返回：一次请求本身要花几百毫秒，而「token 在这一刻是否有效」
		// 是服务端**处理时**的判断。只记一个时刻的话，那几百毫秒的往返会被
		// 误读成「宽限期」—— 这正是本实验第一版犯的错。
		at   time.Duration
		done time.Duration
		err  error
	}
	var (
		polls     []poll
		login     loginResult
		loginSeen bool
		firstFail = time.Duration(-1)
	)

	// pollOnce 打一次 token1 并记录。
	pollOnce := func() bool { // 返回 true 表示已被拒
		at := time.Since(tSwitch)
		err := p.probeAuthOnce(first, deviceA)
		polls = append(polls, poll{at: at, done: time.Since(tSwitch), err: err})
		if err != nil && appapi.IsAuthError(err) {
			if firstFail < 0 {
				firstFail = at
			}
			return true
		}
		return false
	}

	// 阶段一：第二次登录还没返回之前，密集探测（100ms 间隔）。
	//
	// 这一段的用处是判断「失效是否发生在登录完成之前」：如果登录请求还在路上
	// token1 就没了，那说明服务端在**处理**登录时就把旧会话清掉了。
	for i := 0; i < 60 && !loginSeen; i++ {
		select {
		case login = <-loginCh:
			loginSeen = true
		default:
		}
		if pollOnce() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 阶段二：登录已返回，但仍可能是「过一会儿才踢」。再密集探一小段。
	if firstFail < 0 {
		for i := 0; i < 12; i++ {
			select {
			case login = <-loginCh:
				loginSeen = true
			default:
			}
			if pollOnce() {
				break
			}
			time.Sleep(250 * time.Millisecond)
		}
	}

	// 阶段三：还没被拒 —— 那就稀疏地看到底（可能真有很长的宽限期）。
	if firstFail < 0 {
		for _, d := range []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 3 * time.Minute} {
			if d > wait {
				break
			}
			if sleep := time.Until(tSwitch.Add(d)); sleep > 0 {
				time.Sleep(sleep)
			}
			if pollOnce() {
				break
			}
		}
	}
	// 登录结果必须被收走，否则 token2 拿不到、goroutine 也悬着。
	if !loginSeen {
		login = <-loginCh
		loginSeen = true
	}

	if login.err != nil {
		return fmt.Errorf("第二次登录失败: %w", login.err)
	}
	second := login.token
	if err := p.checkAuth("token2（刚登录的）", second, deviceB); err != nil {
		fmt.Println("  ⚠️ 新 token 立刻不可用 —— 这本身是异常，请把输出完整保留")
	}
	p.save("session_tokens", map[string]string{
		"token1_suffix": tail(first),
		"token2_suffix": tail(second),
	})

	fmt.Printf("\n  %-24s  %s\n", "token1 的请求窗口", "结果")
	for _, pl := range polls {
		window := fmt.Sprintf("%s→%s", pl.at.Round(time.Millisecond), pl.done.Round(time.Millisecond))
		if pl.err == nil {
			fmt.Printf("  %-24s  ✓ 受理\n", window)
			continue
		}
		if appapi.IsAuthError(pl.err) {
			fmt.Printf("  %-24s  ✗ 被拒（%v）\n", window, pl.err)
			continue
		}
		fmt.Printf("  %-24s  ? 非凭据类错误（不计入判定）：%v\n", window, pl.err)
	}
	fmt.Printf("\n第二次登录返回于 %s（相对本次实验起点）\n", login.at.Round(time.Millisecond))

	// 失效时刻 T 只能被**夹**在一个区间里。两个边界都要按「服务端什么时候做判断」
	// 来取，而不是按「我们什么时候发请求」：
	//
	//	下界：最后一次成功的请求在 [发出, 返回] 之间被服务端判定为有效，
	//	      因此 T **严格大于**它的**发出**时刻。
	//	上界：第一次失败的请求在 [发出, 返回] 之间被判定为无效，
	//	      因此 T **小于等于**它的**返回**时刻。
	//
	// 上界取「发出」时刻是不严谨的 —— 服务端完全可能在我们发出之后才处理它，
	// 那样会把「与登录完成同时」误报成「早于登录完成」。第一版就是这么错的。
	var lastOK, lastOKDone, firstBad, firstBadDone time.Duration = -1, -1, -1, -1
	for _, pl := range polls {
		if pl.err != nil && appapi.IsAuthError(pl.err) {
			firstBad, firstBadDone = pl.at, pl.done
			break
		}
		if pl.err == nil {
			lastOK, lastOKDone = pl.at, pl.done
		}
	}
	p.save("session_timing", map[string]any{
		"login2_returned_after":  login.at.String(),
		"last_ok_request_sent":   lastOK.String(),
		"first_bad_request_sent": firstBad.String(),
		"first_bad_request_done": firstBadDone.String(),
		"bracket":                fmt.Sprintf("(%s, %s]", lastOK, firstBadDone),
		"bracket_width":          (firstBadDone - lastOK).String(),
		"poll_count":             len(polls),
		"wait_budget":            wait.String(),
	})

	fmt.Println()
	if firstBad < 0 {
		fmt.Printf("结论：在 %s 的观察窗口内 token1 **一直可用**。\n", wait)
		fmt.Println("      这不能说成「永不失效」——只能说在这个窗口内没观察到失效。")
		fmt.Println("      若需要明确结论，把 -wait 调大再跑一次。")
		return nil
	}

	fmt.Printf("token1 最后一次被受理的请求：发出于 %s、返回于 %s。\n",
		lastOK.Round(time.Millisecond), lastOKDone.Round(time.Millisecond))
	fmt.Printf("第一次被拒的请求：发出于 %s、返回于 %s。\n",
		firstBad.Round(time.Millisecond), firstBadDone.Round(time.Millisecond))
	fmt.Printf("因此**失效时刻 T 落在 (%s, %s]**，区间宽度 %s = 单次请求往返。\n",
		lastOK.Round(time.Millisecond), firstBadDone.Round(time.Millisecond), (firstBadDone - lastOK).Round(time.Millisecond))

	fmt.Println()
	switch {
	case login.at > firstBadDone:
		fmt.Println("结论：失效发生在第二次登录**返回之前**（T ≤ 上界 < 登录返回时刻）——")
		fmt.Println("      服务端在处理登录请求的过程中就清掉了旧会话。挤掉是**立即**的。")
	case login.at > lastOK:
		// 登录返回时刻落在夹逼区间内。这里**不能**说「挤掉是立即的」——
		// 夹逼只给出上界，而区间宽度正是我们自己的请求往返时间。
		// 把能证明的与不能证明的分开写。
		upper := firstBadDone - login.at
		fmt.Println("结论：第二次登录返回的时刻**落在上面那个区间内**，因此能证明的只有：")
		fmt.Printf("      「旧会话失效」发生在登录返回后**至多 %s** 之内。\n", upper.Round(time.Millisecond))
		fmt.Println()
		fmt.Printf("      这个上界已经小到没有运维含义（重试策略按秒计），所以实践结论是「没有宽限期」。\n")
		fmt.Println("      但严格说，我们**没有证明**它是 0 —— 只证明了它不大于上面这个数。")
		fmt.Println("      要压得更小，只能缩小单次请求的往返 —— 区间宽度就是它。")
	default:
		fmt.Printf("结论：token1 在第二次登录返回后**至少 %s** 仍然可用 —— 存在宽限期。\n",
			(lastOK - login.at).Round(time.Millisecond))
		fmt.Printf("      精确上界需要更密的探测（现在这个区间宽 %s）。\n", (firstBadDone - lastOK).Round(time.Millisecond))
	}

	fmt.Println()
	fmt.Println("对部署的含义：服务端一旦发现 token 失效就必须停止重试 ——")
	fmt.Println("反复重登会与用户手机互相把对方挤下线（auth.md 的「拉锯战」一节）。")
	return nil
}

// 三个设备标识：前两个是固定值（其一来自 DefaultIdentity），第三个在运行时随机。
// 固定值是为了让两次复勘可比；随机值是为了排除「服务端只认某个白名单」。
const (
	deviceA = "11111111-2222-3333-4444-555555555555"
	deviceB = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

// login 用给定的 device_uuid 登录一次。
func (p *probe) login(username, password, deviceUUID string) (string, error) {
	cl := p.withIdentity(deviceUUID, "")
	token, err := cl.Login(p.ctx, username, password)
	if err != nil {
		return "", fmt.Errorf("登录失败：%w", err)
	}
	return token, nil
}

// withIdentity 复制一份客户端，换上指定的 device_uuid 与 token。
//
// 刻意**复制**而不是改 p.client：探针的每次请求都必须是独立可解释的，
// 一个被就地改过的共享客户端会让「刚才那次用的是哪个 uuid」变成要翻代码才知道的事。
func (p *probe) withIdentity(deviceUUID, token string) *appapi.Client {
	cl := *p.client
	cl.Token = token
	cl.Identity = p.client.Identity
	if deviceUUID != "" {
		cl.Identity.DeviceUUID = deviceUUID
	}
	return &cl
}

// checkAuth 打一次需要凭据的端点并打印结果，返回错误。
func (p *probe) checkAuth(label, token, deviceUUID string) error {
	err := p.probeAuthOnce(token, deviceUUID)
	if err != nil {
		fmt.Printf("  ✗ %-28s %v\n", label, err)
		return err
	}
	fmt.Printf("  ✓ %-28s 受理\n", label)
	return nil
}

// probeAuthOnce 用给定 token 与 device_uuid 打一次**需要凭据**的端点。
//
// 选 /api/v1/users 是因为它最便宜（一次请求、不翻页），而它确实要凭据：
// 匿名请求会得到 JWTVerificationError。
func (p *probe) probeAuthOnce(token, deviceUUID string) error {
	cl := p.withIdentity(deviceUUID, token)
	return cl.GetJSON(p.ctx, "/api/v1/users", nil, nil)
}

// tail 只留 token 的尾部若干字符 —— 证据文件里不该出现完整凭据。
func tail(s string) string {
	if len(s) <= 8 {
		return "…"
	}
	return "…" + s[len(s)-8:]
}

// newUUID 生成一个随机 UUID v4 形态的字符串。
//
// 手写而不引依赖：探针只需要「看起来像另一个设备」，不需要密码学强度。
func newUUID() string {
	b := make([]byte, 16)
	// 用时间与启动纳秒拼一点熵；这里的用途是「换一个不同的值」，不是安全。
	seed := time.Now().UnixNano()
	for i := range b {
		seed = seed*6364136223846793005 + 1442695040888963407
		b[i] = byte(seed >> 33)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
