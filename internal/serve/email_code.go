package serve

import (
	"context"
	"fmt"
	"net/http"

	"ai_analyze_guokao/internal/mail"
	"ai_analyze_guokao/internal/setting"
)

func (s *Server) mailConfig(ctx context.Context) (mail.Config, error) {
	host, err := s.Settings.Get(ctx, setting.KeySMTPHost)
	if err != nil {
		return mail.Config{}, err
	}
	port, err := s.Settings.Get(ctx, setting.KeySMTPPort)
	if err != nil {
		return mail.Config{}, err
	}
	user, err := s.Settings.Get(ctx, setting.KeySMTPUser)
	if err != nil {
		return mail.Config{}, err
	}
	pass, err := s.Settings.Get(ctx, setting.KeySMTPPassword)
	if err != nil {
		return mail.Config{}, err
	}
	from, err := s.Settings.Get(ctx, setting.KeyMailFrom)
	if err != nil {
		return mail.Config{}, err
	}
	return mail.Config{Host: host, Port: port, Username: user, Password: pass, From: from}, nil
}

func (s *Server) emailCode(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &in) {
		return
	}
	id, code, err := s.Study.CreateEmailCode(r.Context(), in.Email)
	if err != nil {
		s.studyError(w, err)
		return
	}
	cfg, err := s.mailConfig(r.Context())
	if err != nil {
		s.Study.DeleteEmailCode(r.Context(), id)
		s.fail(w, "读取邮件配置失败", err)
		return
	}
	if !cfg.Configured() {
		s.Log.Warn("开发降级：邮件验证码仅打印日志", "email", in.Email, "code", code)
		writeJSON(w, 200, map[string]any{"ok": true})
		return
	}
	body := fmt.Sprintf("你的验证码是 %s，5 分钟内有效。若非本人操作，请忽略此邮件。", code)
	if err := mail.Send(r.Context(), cfg, in.Email, "行测研习注册验证码", body); err != nil {
		s.Study.DeleteEmailCode(r.Context(), id)
		writeErr(w, http.StatusBadGateway, "验证码发送失败，请稍后重试")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) handleTestMail(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To string `json:"to"`
	}
	if !decode(w, r, &in) {
		return
	}
	cfg, err := s.mailConfig(r.Context())
	if err != nil {
		s.fail(w, "读取邮件配置失败", err)
		return
	}
	if !cfg.Configured() {
		writeErr(w, http.StatusBadRequest, "请先在设置中配置 SMTP")
		return
	}
	if err := mail.Send(r.Context(), cfg, in.To, "行测研习测试邮件", "这是一封测试邮件，收到即表示邮件服务配置正常。"); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
