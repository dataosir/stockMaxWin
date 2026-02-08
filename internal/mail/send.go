// Package mail 按 SMTP 配置发送选股结果 HTML 邮件。
package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"stockMaxWin/internal/model"
	"stockMaxWin/internal/trace"
)

// 炒股格言（随机发一句，提醒无入选时也保持状态）
var stockMaxims = []string{
	"在别人恐惧时贪婪，在别人贪婪时恐惧。——巴菲特",
	"价格是你付出的，价值是你得到的。——巴菲特",
	"投资最重要的两条：第一，永远不要亏钱；第二，永远不要忘记第一条。——巴菲特",
	"不打算持有十年的股票，不要持有十分钟。——巴菲特",
	"买你理解的东西，不买你不理解的。——彼得·林奇",
	"短期市场是投票机，长期是称重机。——格雷厄姆",
	"市场短期是投票机，长期是称重机。——本杰明·格雷厄姆",
	"风险来自你不知道自己在做什么。——巴菲特",
	"宁可错过，不可做错。没有符合条件就不出手。——常见纪律",
	"会空仓的是师爷，会等待的是高手。",
}

// 启动问候「加油的话」（每次随机一句）
var greetingCheers = []string{
	"新的一天，稳住心态，按纪律来。",
	"行情在变，策略不变，加油。",
	"少动多看，等信号再出手。",
	"今天也要理性交易，不追高不杀跌。",
	"做好功课，机会来了才接得住。",
	"控制仓位，保住本金，来日方长。",
	"早盘先看大盘，再选个股，稳。",
	"宁可少赚，不可大亏，共勉。",
	"坚持纪律，时间会站在你这边。",
	"早，今天也要认真复盘、冷静下单。",
}

// 超时与端口
const (
	smtpTimeout      = 15 * time.Second
	defaultSMTPPort  = 587
	smtpPortTLS      = 465
)

// 邮件主题与内容
const (
	subjectReport       = "今日选股结果"
	subjectNoSelection  = "选股提醒：本期无入选，请好好工作"
	subjectStartup      = "选股助手已启动 · 今日大盘"
	titleReport         = "选股结果"
	titleNoSelection    = "选股提醒"
	titleStartup        = "选股助手已启动"
	htmlCharset         = "UTF-8"
	emptyMainBusiness   = "-"
)

type SMTPConfig struct {
	Server   string
	Port     int
	User     string
	Password string
	From     string
	To       string
}

func (s *SMTPConfig) Enabled() bool {
	return strings.TrimSpace(s.Server) != "" &&
		strings.TrimSpace(s.From) != "" &&
		strings.TrimSpace(s.To) != ""
}

func SendReport(ctx context.Context, cfg *SMTPConfig, stocks []*model.Stock) error {
	if cfg == nil || !cfg.Enabled() {
		return nil
	}
	if len(stocks) == 0 {
		return nil
	}
	trace.Log(ctx, "mail: SendReport to=%s count=%d", cfg.To, len(stocks))
	body := buildHTMLTable(stocks)
	subject := subjectReport
	toList := strings.Split(cfg.To, ",")
	for i := range toList {
		toList[i] = strings.TrimSpace(toList[i])
	}
	err := send(cfg, subject, body, toList)
	if err != nil {
		trace.Log(ctx, "mail: send err=%v", err)
		return err
	}
	trace.Log(ctx, "mail: sent ok")
	return nil
}

func buildHTMLTable(stocks []*model.Stock) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="` + htmlCharset + `"><title>` + titleReport + `</title></head><body>`)
	b.WriteString(`<h2>今日选股结果（按涨幅排序取前10）</h2><p>剔除ST/退市·市值&gt;50亿·PE 0-60·站上MA20·MA60向上·MACD红柱增或金叉·换手3%-10%·量比&gt;1.2。</p>`)
	b.WriteString(`<table border="1" cellspacing="0" cellpadding="8" style="border-collapse: collapse; font-size: 14px;">`)
	b.WriteString(`<thead><tr style="background: #eee;"><th>代码</th><th>名称</th><th>涨幅%</th><th>主营领域</th></tr></thead><tbody>`)
	for _, s := range stocks {
		if s == nil {
			continue
		}
		mb := s.MainBusiness
		if mb == "" {
			mb = emptyMainBusiness
		}
		b.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%.2f</td><td>%s</td></tr>",
			escapeHTML(s.Code), escapeHTML(s.Name), s.ChangePct, escapeHTML(mb)))
	}
	b.WriteString("</tbody></table></body></html>")
	return b.String()
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func send(cfg *SMTPConfig, subject, htmlBody string, to []string) error {
	port := cfg.Port
	if port == 0 {
		port = defaultSMTPPort
	}
	addr := net.JoinHostPort(cfg.Server, strconv.Itoa(port))

	var conn net.Conn
	var err error
	if port == smtpPortTLS {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: smtpTimeout}, "tcp", addr, &tls.Config{ServerName: cfg.Server})
	} else {
		conn, err = net.DialTimeout("tcp", addr, smtpTimeout)
	}
	if err != nil {
		return fmt.Errorf("smtp dial: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, cfg.Server)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if port != smtpPortTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: cfg.Server}); err != nil {
				return fmt.Errorf("starttls: %w", err)
			}
		}
	}

	if cfg.Password != "" {
		auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Server)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, t := range to {
		if t == "" {
			continue
		}
		if err := client.Rcpt(t); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", t, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	headers := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nContent-Type: text/html; charset=UTF-8\r\n\r\n",
		cfg.From, strings.Join(to, ","), subject)
	if _, err := w.Write([]byte(headers + htmlBody)); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close: %w", err)
	}
	return client.Quit()
}

func MustSendReport(ctx context.Context, cfg *SMTPConfig, stocks []*model.Stock) {
	if cfg == nil || !cfg.Enabled() {
		if len(stocks) == 0 {
			trace.Log(ctx, "mail: 无选中且未配置 SMTP，跳过")
		}
		return
	}
	if len(stocks) == 0 {
		trace.Log(ctx, "mail: 无选中股票，按设计不发邮件（正常）")
		return
	}
	if err := SendReport(ctx, cfg, stocks); err != nil {
		trace.Log(ctx, "mail: 发送失败 err=%v", err)
		return
	}
	trace.Log(ctx, "mail: 已发送 to=%s count=%d", cfg.To, len(stocks))
}

// SendNoSelectionReminder 连续多次无入选时发送提醒：本期没有入选股票，请好好工作 + 随机一句炒股格言。
func SendNoSelectionReminder(ctx context.Context, cfg *SMTPConfig) error {
	if cfg == nil || !cfg.Enabled() {
		return nil
	}
	quote := stockMaxims[rand.Intn(len(stockMaxims))]
	trace.Log(ctx, "mail: 发送无入选提醒，格言=%s", quote)
	body := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="%s"><title>%s</title></head><body>
<h2>本期没有入选股票</h2>
<p>请好好工作，耐心等待符合条件的机会。</p>
<p style="margin-top:16px;color:#666;font-style:italic;">%s</p>
</body></html>`, htmlCharset, titleNoSelection, escapeHTML(quote))
	subject := subjectNoSelection
	toList := strings.Split(cfg.To, ",")
	for i := range toList {
		toList[i] = strings.TrimSpace(toList[i])
	}
	return send(cfg, subject, body, toList)
}

// SendStartupGreeting 启动成功时发送打招呼邮件：今日大盘数据 + 随机一句加油的话。
func SendStartupGreeting(ctx context.Context, cfg *SMTPConfig, indices []model.IndexQuote) error {
	if cfg == nil || !cfg.Enabled() {
		return nil
	}
	cheer := greetingCheers[rand.Intn(len(greetingCheers))]
	trace.Log(ctx, "mail: 发送启动问候 to=%s 加油=%s", cfg.To, cheer)
	body := buildStartupGreetingHTML(indices, cheer)
	toList := strings.Split(cfg.To, ",")
	for i := range toList {
		toList[i] = strings.TrimSpace(toList[i])
	}
	return send(cfg, subjectStartup, body, toList)
}

func buildStartupGreetingHTML(indices []model.IndexQuote, cheer string) string {
	var b strings.Builder
	// 现代邮件风格：窄幅、留白、无衬线字体、涨跌颜色
	b.WriteString(`<!DOCTYPE html><html><head><meta charset="` + htmlCharset + `"><meta name="viewport" content="width=device-width,initial-scale=1">`)
	b.WriteString(`<title>` + titleStartup + `</title></head><body style="margin:0;padding:0;background:#f5f5f5;font-family:-apple-system,BlinkMacSystemFont,Segoe UI,Roboto,Helvetica Neue,sans-serif;">`)
	b.WriteString(`<div style="max-width:520px;margin:24px auto;padding:28px 24px;background:#fff;border-radius:12px;box-shadow:0 2px 12px rgba(0,0,0,.06);">`)
	b.WriteString(`<h1 style="margin:0 0 8px;font-size:20px;font-weight:600;color:#1a1a1a;">选股助手已启动</h1>`)
	b.WriteString(`<p style="margin:0 0 20px;font-size:14px;color:#666;">下面是今日大盘，之后会按 9:15～15:00 每半小时跑一次选股（工作日）。</p>`)
	b.WriteString(`<table style="width:100%;border-collapse:collapse;font-size:14px;">`)
	b.WriteString(`<thead><tr style="border-bottom:2px solid #eee;"><th style="text-align:left;padding:12px 10px;color:#666;font-weight:500;">指数</th><th style="text-align:right;padding:12px 10px;color:#666;font-weight:500;">现价</th><th style="text-align:right;padding:12px 10px;color:#666;font-weight:500;">涨跌幅</th></tr></thead><tbody>`)
	for i, q := range indices {
		bg := "#fff"
		if i%2 == 1 {
			bg = "#fafafa"
		}
		pctStyle := "color:#333;"
		if q.ChangePct > 0 {
			pctStyle = "color:#c62828;"
		} else if q.ChangePct < 0 {
			pctStyle = "color:#2e7d32;"
		}
		pctStr := fmt.Sprintf("%.2f%%", q.ChangePct)
		b.WriteString(fmt.Sprintf(`<tr style="background:%s"><td style="padding:12px 10px;color:#1a1a1a;">%s</td><td style="text-align:right;padding:12px 10px;color:#1a1a1a;">%.2f</td><td style="text-align:right;padding:12px 10px;%s">%s</td></tr>`,
			bg, escapeHTML(q.Name), q.Price, pctStyle, pctStr))
	}
	b.WriteString("</tbody></table>")
	b.WriteString(`<p style="margin:22px 0 0;padding:14px 16px;background:#f8f9fa;border-radius:8px;font-size:14px;color:#374151;line-height:1.5;">` + escapeHTML(cheer) + `</p>`)
	b.WriteString(`<p style="margin:20px 0 0;font-size:12px;color:#9ca3af;">本邮件由选股助手自动发送，请勿直接回复。</p>`)
	b.WriteString(`</div></body></html>`)
	return b.String()
}
