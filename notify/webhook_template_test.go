package notify

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// This file locks down the **capability boundary** of the generic Webhook template.
//
// It is the only place in the whole package where "a user-provided string is evaluated as code", so it must be clear what it can do and
// what it cannot, and these properties are pinned down by tests - otherwise if someone casually adds a
// method to the template context, or adds a readFile to the FuncMap, the capability surface would silently expand, while the diff looks like
// just a harmless little function.

// TestTemplateContextHasNoMethods is the most important one.
//
// text/template calls exported methods ({{.Foo}} can fetch a field as well as call a method). So as long as the template context
// can reach **any** type with exported methods, those methods are exposed to the template author.
// The context of this feature is deliberately pure data (exported fields only, zero methods).
//
// If this fails: someone added a method to webhookTemplateData / webhookItem.
// Before deciding to allow it, think carefully about whether the template could use that method to read something that is not meant to be exposed.
func TestTemplateContextHasNoMethods(t *testing.T) {
	for _, v := range []any{webhookTemplateData{}, webhookItem{}} {
		typ := reflect.TypeOf(v)
		if n := typ.NumMethod(); n != 0 {
			var names []string
			for i := 0; i < n; i++ {
				names = append(names, typ.Method(i).Name)
			}
			t.Fatalf("%s 暴露了 %d 个方法（%s）：text/template 可以调用它们，"+
				"等于把这些方法的能力开放给了模板作者", typ.Name(), n, strings.Join(names, ", "))
		}
	}
}

// TestTemplateFuncsAreMinimal pins down the set of functions exposed to templates.
//
// Every extra function in the FuncMap adds a capability. Currently there are only json / jsons, which serialize a value
// into a JSON fragment - they cannot read files, send requests or execute commands.
func TestTemplateFuncsAreMinimal(t *testing.T) {
	var got []string
	for name := range webhookTemplateFuncs {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"json", "jsons"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("模板函数集合变了：得到 %v，期望 %v。新增函数前请确认它不会扩大能力面"+
			"（不能读写文件、不能发起网络请求、不能执行命令）", got, want)
	}
}

// TestTemplateCannotReachUnknownData covers out-of-bounds access in templates:
// accessing something that does not exist must fail rather than echo something back, and the failure message must not leak internal data.
func TestTemplateCannotReachUnknownData(t *testing.T) {
	_, err := renderWebhookBody(`{"x": {{.Environment}}, "y": {{.Env}}}`, singleMsg())
	if err == nil {
		t.Fatal("访问不存在的字段应报错")
	}
	// The error must not contain real content from the template context (vulnerability title/summary).
	for _, leak := range []string{"SQL注入", "参数 id"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("模板错误泄露了消息内容 %q: %v", leak, err)
		}
	}
}

// TestTemplateRenderFailsPermanently: a wrong template is a configuration error, and retrying will not heal it.
// If it were judged retryable, one bad template would make every delivery waste three rounds of backoff.
func TestTemplateRenderFailsPermanently(t *testing.T) {
	cfg := map[string]any{
		"url":           "https://example.com/hook",
		"body_template": `{{.Items.`,
	}
	if err := (webhookChannel{}).Validate(cfg); err == nil {
		t.Fatal("模板语法错误应在保存时就被拦下")
	}
	// Even if validation is bypassed and delivery happens directly, it must be judged a permanent failure rather than retried repeatedly.
	_, err := (webhookChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("坏模板应判永久失败，得到 %v", err)
	}
}

// TestTemplateCanOnlyProduceJSON covers the constraint that "the template render result must be valid JSON".
// It also blocks usages such as "generating plain text with a template to trigger another protocol".
func TestTemplateCanOnlyProduceJSON(t *testing.T) {
	// A valid template passes.
	ok := map[string]any{"url": "https://example.com/hook", "body_template": `{"t":{{json .Title}}}`}
	if err := (webhookChannel{}).Validate(ok); err != nil {
		t.Fatalf("合法模板应通过校验: %v", err)
	}
	// When the render is not JSON it must be rejected (rather than sent out as is).
	bad := map[string]any{"url": "http://127.0.0.1:1/hook", "body_template": `not json {{.Count}}`}
	_, err := (webhookChannel{}).Send(context.Background(), bad, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("渲染出非 JSON 应判永久失败，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "올바른 JSON") {
		t.Errorf("错误信息应说明是 JSON 问题，得到 %v", err)
	}
}
