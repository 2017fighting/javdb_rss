package catalog

import (
	"context"
	"net/url"
)

// Source 产出 feed 所需要的作品数据。
//
// 它是本服务的**唯一边界端口**：HTTP 层只认这个接口，不知道背后是真实的
// App 私有 API、一个磁盘缓存、还是一个测试用的假实现。ticket 09 若要引入
// 缓存或后台刷新，就在这一层包一层装饰器，上层完全不动。
//
// 实现方需要自己处理「给一个番号，怎么找出是哪部作品」这个问题
// （归 ticket 06）。本接口刻意不暴露 movie id 之类上游标识，
// 正是因为那套规则还没定下来。
type Source interface {
	// Code 返回某个番号对应的作品。
	//
	// 之所以返回切片而不是单个 Work：番号在 App API 里**不是唯一键**
	// （合集、不同片商同名等情况），一个番号可能对应多部作品。
	// 如何从中消歧属于 ticket 06；在规则定下来之前，实现方应当原样返回候选，
	// 由上层如实呈现。
	Code(ctx context.Context, code string) ([]Work, error)

	// Actress 返回某个女优的作品列表。
	//
	// params 是**原样透传**给 App 演员页的查询参数（用户已选定这个做法）。
	// 本服务不解释、不改写、不校验这些参数 —— 它们是上游的私有契约，
	// 我们只负责搬运。`since` 一类的本服务自有参数应由调用方在进到这里之前摘除。
	Actress(ctx context.Context, id string, params url.Values) ([]Work, error)
}
