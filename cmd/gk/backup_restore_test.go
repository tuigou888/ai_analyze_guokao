package main

import (
	"ai_analyze_guokao/internal/serve"
	"ai_analyze_guokao/internal/setting"
	"ai_analyze_guokao/internal/store"
	"ai_analyze_guokao/internal/study"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBackupRestorePreservesPracticeAndMatchedKey(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.sqlite")
	key := filepath.Join(dir, "source.key")
	snapshot := filepath.Join(dir, "restored.sqlite")
	restoredKey := filepath.Join(dir, "restored.key")
	db, e := store.Open(source)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	settings, e := setting.Open(db, key)
	if e != nil {
		t.Fatal(e)
	}
	if e = settings.Set(ctx, setting.KeyAPIKey, "synthetic-backup-key", "test"); e != nil {
		t.Fatal(e)
	}
	s := &study.Service{DB: db}
	u, e := s.Register(ctx, "restore_user", "testpassword", "test")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`INSERT INTO question(id,content_hash,stem,answer,answer_type,option_count) VALUES(1,'restore','stem','B','single',1);INSERT INTO option(question_id,ord,label,content) VALUES(1,1,'B','option')`); e != nil {
		t.Fatal(e)
	}
	p, e := s.CreateSession(ctx, u.ID, "single", study.Spec{QuestionIDs: []int64{1}})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Submit(ctx, u.ID, p.ID, []study.Answer{{QuestionID: 1, Answer: "B"}}, 0); e != nil {
		t.Fatal(e)
	}
	if e = cmdBackup([]string{"--db", source, "--out", snapshot}); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(key)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(restoredKey, b, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`DELETE FROM practice_answer;DELETE FROM practice_session`); e != nil {
		t.Fatal(e)
	}
	restored, e := store.Open(snapshot)
	if e != nil {
		t.Fatal(e)
	}
	defer restored.Close()
	rs, e := setting.Open(restored, restoredKey)
	if e != nil {
		t.Fatal(e)
	}
	if v, e := rs.Get(ctx, setting.KeyAPIKey); e != nil || v != "synthetic-backup-key" {
		t.Fatal("matched key did not restore setting", e)
	}
	r, e := (&study.Service{DB: restored}).Session(ctx, u.ID, p.ID)
	if e != nil || r.Correct != 1 || len(r.Answers) != 1 || r.Answers[0].Answer != "B" {
		t.Fatalf("practice not restored: %+v %v", r, e)
	}
	wrong := filepath.Join(dir, "wrong.key")
	if e = os.WriteFile(wrong, []byte(strings.Repeat("0", 64)), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = setting.Open(restored, wrong); e == nil {
		t.Fatal("mismatched backup key accepted")
	}
}

// Complete v12 recovery must preserve preferences, the original login Cookie,
// public session metadata and submitted records, then allow another CAS write.
func TestPersonalBackupBundleRestoresProfileSessionAndCAS(t *testing.T) {
	t.Setenv("GK_SECRET_KEY", "")
	dir := t.TempDir()
	source, key, bundle := filepath.Join(dir, "source.sqlite"), filepath.Join(dir, "source.key"), filepath.Join(dir, "bundle")
	db, err := store.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	settings, err := setting.Open(db, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = settings.Set(ctx, setting.KeyAPIKey, "synthetic-personal-restore-key", "test"); err != nil {
		t.Fatal(err)
	}
	s := &study.Service{DB: db}
	u, err := s.Register(ctx, "personal_restore", "synthetic-password", "原昵称")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO question(id,content_hash,stem,answer,answer_type,option_count,module) VALUES(1,'personal-restore','题干','B','single',1,'资料分析'); INSERT INTO option(question_id,ord,label,content) VALUES(1,1,'B','选项')`); err != nil {
		t.Fatal(err)
	}
	in := study.ProfileUpdate{Nickname: "恢复昵称", Bio: "非默认资料", AvatarID: 7, DailyQuestions: 50, DailyMinutes: 90, ExamName: "备考计划", ExamDate: time.Now().In(time.FixedZone("CST", 8*3600)).AddDate(1, 0, 0).Format("2006-01-02"), DefaultLimit: 50, DefaultModule: "资料分析", ReadingSize: 20}
	profile, err := s.UpdateProfile(ctx, u.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Revision, in.Bio = profile.Revision, "第二版资料"
	profile, err = s.UpdateProfile(ctx, u.ID, in)
	if err != nil || profile.Revision != 2 {
		t.Fatal("source profile CAS", profile, err)
	}
	token, _, err := s.LoginWithMetadata(ctx, u.Username, "synthetic-password", study.LoginMetadata{DeviceLabel: "Chrome / Linux", IP: "203.0.113.42"})
	if err != nil {
		t.Fatal(err)
	}
	loginSessions, err := s.Sessions(ctx, u.ID, token, 1)
	if err != nil || len(loginSessions.Items) != 1 {
		t.Fatal("source sessions", loginSessions, err)
	}
	practice, err := s.CreateSession(ctx, u.ID, "single", study.Spec{QuestionIDs: []int64{1}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Submit(ctx, u.ID, practice.ID, []study.Answer{{QuestionID: 1, Answer: "B"}}, 0); err != nil {
		t.Fatal(err)
	}
	if err = cmdBackup([]string{"--db", source, "--bundle", bundle, "--secret-key-file", key}); err != nil {
		t.Fatal(err)
	}
	if err = cmdBackup([]string{"--verify-bundle", bundle}); err != nil {
		t.Fatal(err)
	}
	// Restore a copy: verification and service startup never mutate the backup.
	restoredPath := filepath.Join(dir, "restored.sqlite")
	contents, err := os.ReadFile(filepath.Join(bundle, "gk.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(restoredPath, contents, 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	rs, err := setting.Open(restored, filepath.Join(bundle, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rs.Get(ctx, setting.KeyAPIKey); err != nil || got != "synthetic-personal-restore-key" {
		t.Fatal("restored effective key", err)
	}
	recovered := &study.Service{DB: restored}
	gotSessions, err := recovered.Sessions(ctx, u.ID, token, 1)
	if err != nil || !reflect.DeepEqual(gotSessions, loginSessions) {
		t.Fatal("public session metadata changed", err)
	}
	gotPractice, err := recovered.Session(ctx, u.ID, practice.ID)
	if err != nil || gotPractice.SubmittedAt == "" || gotPractice.Correct != 1 || len(gotPractice.Answers) != 1 || gotPractice.Answers[0].Answer != "B" {
		t.Fatal("submitted practice lost", gotPractice, err)
	}
	h := serve.New(rs, restored, nil).Routes()
	request := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "gk_user", Value: token})
		if method == "PUT" {
			r.Header.Set("Origin", "http://example.com")
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/api/account/profile", "")
	var gotProfile study.Profile
	if err = json.Unmarshal(w.Body.Bytes(), &gotProfile); w.Code != 200 || err != nil || !reflect.DeepEqual(gotProfile, profile) {
		t.Fatal("original Cookie/profile not restored", w.Code, w.Body.String(), err)
	}
	w = request("GET", "/api/auth/me", "")
	var me study.User
	if err = json.Unmarshal(w.Body.Bytes(), &me); w.Code != 200 || err != nil || me.ID != u.ID || me.Username != u.Username || me.Nickname != profile.Nickname {
		t.Fatal("restored identity", w.Code, w.Body.String(), err)
	}
	in.Revision, in.Bio = profile.Revision, "恢复后再次更新"
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	w = request("PUT", "/api/account/profile", string(body))
	if err = json.Unmarshal(w.Body.Bytes(), &gotProfile); w.Code != 200 || err != nil || gotProfile.Revision != 3 || gotProfile.Bio != in.Bio || gotProfile.DefaultLimit != 50 || gotProfile.ReadingSize != 20 {
		t.Fatal("post-restore CAS failed", w.Code, w.Body.String(), err)
	}
	w = request("PUT", "/api/account/profile", string(body))
	if w.Code != 409 {
		t.Fatal("post-restore stale CAS accepted", w.Code, w.Body.String())
	}
	if err = cmdBackup([]string{"--verify-bundle", bundle}); err != nil {
		t.Fatal("restore mutated original bundle", err)
	}
}
