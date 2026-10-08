// Package store 负责 SQLite 持久化。P0 只建数据层表；
// 蒸馏层（label）与聚合层（concept）由后续迁移追加，避免建一堆空表。
package store

import (
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite" // 纯 Go 驱动，免 CGO，交叉编译友好
)

// migrations 按版本顺序追加，不可修改已发布的条目。
var migrations = []string{
	// v1：数据层（L1/L2）
	`
CREATE TABLE paper (
  id             INTEGER PRIMARY KEY,
  name           TEXT NOT NULL,
  region         TEXT,
  year           INTEGER,
  module         TEXT,
  exam_type      TEXT,
  paper_variant  TEXT,
  declared_count INTEGER,
  question_count INTEGER,
  source_path    TEXT NOT NULL UNIQUE
);
CREATE INDEX idx_paper_filter ON paper(exam_type, year, module, region);

CREATE TABLE material (
  id       INTEGER PRIMARY KEY,
  paper_id INTEGER NOT NULL REFERENCES paper(id) ON DELETE CASCADE,
  seq      INTEGER NOT NULL,
  body     TEXT,
  body_html TEXT,
  has_figure INTEGER NOT NULL DEFAULT 0,
  UNIQUE(paper_id, seq)
);

-- question 是按 content_hash 去重后的题目实体。
-- 源数据里 58890 个题块只对应 27448 道真题（重复率 53.4%，跨省联考造成）。
CREATE TABLE question (
  id                  INTEGER PRIMARY KEY,
  content_hash        TEXT NOT NULL UNIQUE,
  qid                 TEXT,
  module              TEXT,
  stem                TEXT,
  stem_html           TEXT,
  answer              TEXT,
  answer_type         TEXT NOT NULL,
  answer_mark         TEXT,             -- 由 ✅ 标记推出的答案，用于与 answer 互校
  explanation         TEXT,
  explanation_html    TEXT,
  has_figure          INTEGER NOT NULL DEFAULT 0,
  has_material_figure INTEGER NOT NULL DEFAULT 0,
  option_placeholder  INTEGER NOT NULL DEFAULT 0,
  option_count        INTEGER NOT NULL DEFAULT 0,
  image_count         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_question_module ON question(module);
CREATE INDEX idx_question_qid    ON question(qid);

-- question_occurrence 是题目在具体卷子里的每一次出现。
-- 重复不能删：「这道题被 11 个省考过」本身就是高频考点信号。
CREATE TABLE question_occurrence (
  id           INTEGER PRIMARY KEY,
  question_id  INTEGER NOT NULL REFERENCES question(id) ON DELETE CASCADE,
  paper_id     INTEGER NOT NULL REFERENCES paper(id) ON DELETE CASCADE,
  material_id  INTEGER REFERENCES material(id) ON DELETE SET NULL,
  number       INTEGER NOT NULL,
  raw_qid      TEXT,
  raw_tag      TEXT,
  in_material  INTEGER NOT NULL DEFAULT 0,
  UNIQUE(paper_id, number)
);
CREATE INDEX idx_occ_question ON question_occurrence(question_id);
CREATE INDEX idx_occ_paper    ON question_occurrence(paper_id);

CREATE TABLE option (
  id          INTEGER PRIMARY KEY,
  question_id INTEGER NOT NULL REFERENCES question(id) ON DELETE CASCADE,
  ord         INTEGER NOT NULL,
  label       TEXT NOT NULL,
  content     TEXT,
  content_html TEXT,
  is_correct  INTEGER NOT NULL DEFAULT 0,
  UNIQUE(question_id, label)
);

-- image 的引用计数与出现位置，决定后续公式图 OCR 的优先级。
CREATE TABLE image (
  url          TEXT PRIMARY KEY,         -- 归一化相对路径：题目图/xxx.png
  kind         TEXT NOT NULL,            -- 题目图 / 公式图
  name         TEXT NOT NULL,
  ref_count    INTEGER NOT NULL DEFAULT 0,
  in_stem      INTEGER NOT NULL DEFAULT 0,
  in_option    INTEGER NOT NULL DEFAULT 0,
  in_material  INTEGER NOT NULL DEFAULT 0,
  in_explanation INTEGER NOT NULL DEFAULT 0,
  sha256       TEXT,
  ocr_tex      TEXT,
  ocr_status   TEXT
);
CREATE INDEX idx_image_kind ON image(kind);

-- 解析异常必须落库，不能只打日志：111 道判断题占位、4 道答案缺失、
-- 2 道五/八选项这类边界都靠它暴露。
CREATE TABLE parse_warning (
  id          INTEGER PRIMARY KEY,
  source_path TEXT NOT NULL,
  line        INTEGER,
  kind        TEXT NOT NULL,
  detail      TEXT
);
CREATE INDEX idx_warning_kind ON parse_warning(kind);

	CREATE TABLE ingest_run (
	  id          INTEGER PRIMARY KEY,
	  started_at  TEXT NOT NULL,
	  finished_at TEXT,
	  files       INTEGER NOT NULL DEFAULT 0,
	  questions   INTEGER NOT NULL DEFAULT 0,
	  occurrences INTEGER NOT NULL DEFAULT 0,
	  warnings    INTEGER NOT NULL DEFAULT 0,
	  note        TEXT
	);
	`,

	// v2：P2 公式还原。图片元数据 + OCR 溯源 + 回填后的解析。
	//
	// 关键设计：ocr_tex 存**原始**识别结果，回填时才算展示形态。
	// 这样展示启发式（是否要包 $...$）改错了可以只重跑 backfill，不必重跑 60 分钟的 OCR。
	`
ALTER TABLE image ADD COLUMN bytes INTEGER;
ALTER TABLE image ADD COLUMN width INTEGER;
ALTER TABLE image ADD COLUMN height INTEGER;
ALTER TABLE image ADD COLUMN ocr_model TEXT;   -- 实际使用的模型名，OCR 结果与模型绑定
ALTER TABLE image ADD COLUMN ocr_at TEXT;
ALTER TABLE image ADD COLUMN ocr_error TEXT;   -- 失败原因；ocr_status='error' 时非空
CREATE INDEX idx_image_ocr ON image(kind, ocr_status);
CREATE INDEX idx_image_sha ON image(sha256);

-- explanation_with_formula 是回填后的解析：公式图占位符被替换成 LaTeX。
-- 保留原 explanation 不动，回填是可重放的派生层。
ALTER TABLE question ADD COLUMN explanation_with_formula TEXT;
ALTER TABLE question ADD COLUMN formula_missing INTEGER NOT NULL DEFAULT 0;

CREATE TABLE media_run (
  id          INTEGER PRIMARY KEY,
  kind        TEXT NOT NULL,          -- index / ocr / backfill
  started_at  TEXT NOT NULL,
  finished_at TEXT,
  images      INTEGER NOT NULL DEFAULT 0,
  ok          INTEGER NOT NULL DEFAULT 0,
  failed      INTEGER NOT NULL DEFAULT 0,
  model       TEXT,
  note        TEXT
);
	`,

	// v3：题目图的文本还原。
	//
	// 公式图走数学识别（oc_tex 存 LaTeX）；题目图走文本识别（ocr_tex 存按坐标
	// 重建出的表格行）。两者共用 ocr_tex 一列，用 ocr_engine 区分来源，
	// 这样回填逻辑不需要为两类图各写一套。
	//
	// 题目图的文本为什么值钱：资料分析的材料表格（"年份 × 指标"矩阵）在源
	// Markdown 里完全是张图片，纯文本版本读不到任何数据——这正是资料分析
	// 疑点率高达 28.4% 的根本原因。
	`
ALTER TABLE image ADD COLUMN ocr_engine TEXT;   -- formula | text

ALTER TABLE material ADD COLUMN body_with_text TEXT;  -- 材料正文，题面图占位符替换为识别文本
ALTER TABLE question ADD COLUMN stem_with_text TEXT;  -- 题干，题面图占位符替换为识别文本
ALTER TABLE question ADD COLUMN figure_missing INTEGER NOT NULL DEFAULT 0;
	`,

	// v4：修正历史数据。ocr_engine 是 v3 才加的列，在此之前跑过的公式识别
	// 没有记录引擎；按"有识别结果且 kind=公式图"判定为 formula。
	// 不加这一步，loadOCR 只能靠"不等于 text 就当公式"来兜底，数据不自描述。
	`
UPDATE image SET ocr_engine = 'formula'
 WHERE ocr_engine IS NULL AND ocr_status = 'ok' AND kind = '公式图';
	`,

	// v5：配置、管理员与蒸馏层。
	//
	// setting 存运行时配置（API 地址、key、模型等），让管理入口能改，而不是只能改环境变量。
	// value_secret 是密文（见 internal/setting），明文一律不进库。
	`
CREATE TABLE setting (
  key          TEXT PRIMARY KEY,
  value        TEXT,                 -- 非敏感值
  value_secret BLOB,                 -- 敏感值密文
  is_secret    INTEGER NOT NULL DEFAULT 0,
  updated_at   TEXT,
  updated_by   TEXT
);

-- 管理员：这是运维入口，不是产品账号，因此与后续用户体系分开。
CREATE TABLE admin_user (
  id            INTEGER PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at    TEXT,
  last_login_at TEXT
);

CREATE TABLE admin_session (
  token      TEXT PRIMARY KEY,
  admin_id   INTEGER NOT NULL REFERENCES admin_user(id) ON DELETE CASCADE,
  created_at TEXT,
  expires_at TEXT
);

-- label_run 是一次蒸馏批次。模型溯源放这里：光记配置里的模型名证明不了请求实际
-- 路由到哪个模型（参考文档 §3.7），所以配置名与回执名都要留。
CREATE TABLE label_run (
  id               TEXT PRIMARY KEY,
  scope            TEXT,             -- JSON：题集过滤条件
  prompt_version   TEXT,
  taxonomy_version TEXT,
  model_config     TEXT,             -- 配置里写的模型名
  model_response   TEXT,             -- API 回执里的模型名
  base_url         TEXT,
  status           TEXT NOT NULL,    -- running / finished / failed
  started_at       TEXT,
  finished_at      TEXT,
  total            INTEGER NOT NULL DEFAULT 0,
  ok               INTEGER NOT NULL DEFAULT 0,
  failed           INTEGER NOT NULL DEFAULT 0,
  tokens_in        INTEGER NOT NULL DEFAULT 0,
  tokens_out       INTEGER NOT NULL DEFAULT 0,
  cost_usd         REAL NOT NULL DEFAULT 0,
  note             TEXT
);

-- label 是蒸馏产出。三级考点与考点细节**物理分列**是 §6.3 的核心约束：
-- 混在一列会让 1:1 率飙到 90%+，考点彻底无法聚合。
CREATE TABLE label (
  id               INTEGER PRIMARY KEY,
  question_id      INTEGER NOT NULL REFERENCES question(id) ON DELETE CASCADE,
  run_id           TEXT NOT NULL REFERENCES label_run(id) ON DELETE CASCADE,
  subject          TEXT,   -- 一级科目
  secondary        TEXT,   -- 二级题型
  tertiary         TEXT,   -- 三级考点（纯考点名 + 大类前缀，不带本题做法）
  detail           TEXT,   -- 考点细节（本题在该考点下的具体特征）
  question_model   TEXT,   -- 问法模型
  reasoning_chain  TEXT,   -- JSON 数组
  fastest_solution TEXT,
  pitfalls         TEXT,   -- JSON 数组
  template         TEXT,   -- 母题抽象
  key_features     TEXT,   -- JSON 数组
  boundary         TEXT,   -- 适用边界
  confusable       TEXT,   -- JSON：[{考点, 区分信号}]
  typical_ask      TEXT,   -- JSON 数组
  doubt            TEXT,   -- 疑点：安全阀，模型与官方解析矛盾时只写这里
  tokens_in        INTEGER NOT NULL DEFAULT 0,
  tokens_out       INTEGER NOT NULL DEFAULT 0,
  latency_ms       INTEGER NOT NULL DEFAULT 0,
  created_at       TEXT,
  UNIQUE(question_id, run_id)
);
CREATE INDEX idx_label_tertiary ON label(tertiary);
CREATE INDEX idx_label_run      ON label(run_id);
	`,

	// v6：多模型并发的溯源。
	//
	// 参考文档 §3.10 的硬要求：混用来源的数据**必须标记产地**，否则下游无法过滤与回溯，
	// "质量口径会永久混在一起"。跨模型并行正是一次典型的混用，所以每个标注都要记住
	// 它出自哪个模型。label_run.engines 再记一份按模型聚合的用量，便于横向比较。
	`
ALTER TABLE label ADD COLUMN model TEXT;
ALTER TABLE label_run ADD COLUMN engines TEXT;
CREATE INDEX idx_label_model ON label(model);
	`,
	// v7: 在线账户、冻结练习、轻量考点映射和中文搜索。答案不进入搜索索引。
	`
CREATE TABLE app_user (
 id INTEGER PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_hash TEXT NOT NULL,
 nickname TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, last_login_at TEXT
);
CREATE TABLE app_session (
 token TEXT PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
 expires_at TEXT NOT NULL
);
CREATE INDEX idx_app_session_expiry ON app_session(expires_at);
CREATE TABLE concept (
 id INTEGER PRIMARY KEY, name TEXT NOT NULL, module TEXT NOT NULL, secondary TEXT NOT NULL,
 question_count INTEGER NOT NULL DEFAULT 0, UNIQUE(module, name)
);
CREATE VIEW latest_label AS
SELECT l.*
FROM label l
JOIN (
  SELECT question_id, MAX(id) AS max_id
  FROM label
  GROUP BY question_id
) m ON l.question_id = m.question_id AND l.id = m.max_id;
CREATE TABLE practice_session (
 id INTEGER PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES app_user(id), kind TEXT NOT NULL,
 spec TEXT NOT NULL, question_ids TEXT NOT NULL, started_at TEXT NOT NULL, submitted_at TEXT,
 total INTEGER NOT NULL DEFAULT 0, correct INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER NOT NULL DEFAULT 0, draft TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX idx_practice_user ON practice_session(user_id,id DESC);
CREATE TABLE practice_answer (
 id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL REFERENCES practice_session(id),
 question_id INTEGER NOT NULL REFERENCES question(id), ord INTEGER NOT NULL,
 user_answer TEXT NOT NULL, is_correct INTEGER NOT NULL, duration_ms INTEGER NOT NULL,
 answered_at TEXT NOT NULL, UNIQUE(session_id,ord)
);
CREATE INDEX idx_answer_question ON practice_answer(question_id,session_id);
CREATE TABLE wrongbook (
 user_id INTEGER NOT NULL REFERENCES app_user(id), question_id INTEGER NOT NULL REFERENCES question(id),
 source TEXT NOT NULL, wrong_count INTEGER NOT NULL DEFAULT 0, last_wrong_at TEXT NOT NULL,
 resolved INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(user_id,question_id)
);
CREATE TABLE user_concept_stat (
 user_id INTEGER NOT NULL REFERENCES app_user(id), concept_id INTEGER NOT NULL REFERENCES concept(id),
 answered INTEGER NOT NULL, correct INTEGER NOT NULL, mastery REAL NOT NULL, updated_at TEXT NOT NULL,
 PRIMARY KEY(user_id,concept_id)
);
CREATE VIRTUAL TABLE question_fts USING fts5(stem, content='question', content_rowid='id', tokenize='trigram');
INSERT INTO question_fts(question_fts) VALUES('rebuild');
CREATE TRIGGER question_fts_insert AFTER INSERT ON question BEGIN
 INSERT INTO question_fts(rowid,stem) VALUES(new.id,new.stem);
END;
CREATE TRIGGER question_fts_delete AFTER DELETE ON question BEGIN
 INSERT INTO question_fts(question_fts,rowid,stem) VALUES('delete',old.id,old.stem);
END;
 CREATE TRIGGER question_fts_update AFTER UPDATE OF stem ON question BEGIN
  INSERT INTO question_fts(question_fts,rowid,stem) VALUES('delete',old.id,old.stem);
  INSERT INTO question_fts(rowid,stem) VALUES(new.id,new.stem);
 END;
 	`,
	// v8：错题与收藏分开。wrongbook 只存自动错题，favorites 存用户主动收藏。
	`
 CREATE TABLE favorite (
  user_id INTEGER NOT NULL REFERENCES app_user(id) ON DELETE CASCADE,
  question_id INTEGER NOT NULL REFERENCES question(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  PRIMARY KEY(user_id, question_id)
 );
 CREATE INDEX idx_favorite_user ON favorite(user_id);
 CREATE INDEX idx_favorite_q ON favorite(question_id);
 -- 把原 wrongbook 中 source='manual' 的收藏项迁移到 favorites。
 INSERT INTO favorite(user_id,question_id,created_at) SELECT user_id,question_id,last_wrong_at FROM wrongbook WHERE source='manual';
 -- 保留 source='auto' 的错题记录在 wrongbook 中。
 `,
	// v9：选项 OCR 派生文本；保留原始选项用于页面图片显示。
	`ALTER TABLE option ADD COLUMN content_with_text TEXT;`,
	// v10：每次有效模型响应记账；历史缺口保留并显式标记。
	`ALTER TABLE label_run ADD COLUMN legacy_tokens_in INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE label_run ADD COLUMN legacy_tokens_out INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE label_run ADD COLUMN legacy_cost_usd REAL NOT NULL DEFAULT 0;
	 ALTER TABLE label_run ADD COLUMN usage_complete INTEGER NOT NULL DEFAULT 1;
	 ALTER TABLE label_run ADD COLUMN cost_known INTEGER NOT NULL DEFAULT 0;
	 ALTER TABLE label_run ADD COLUMN input_hash TEXT;
	 UPDATE label_run SET legacy_tokens_in=MAX(tokens_in,(SELECT COALESCE(SUM(tokens_in),0) FROM label WHERE run_id=label_run.id)),legacy_tokens_out=MAX(tokens_out,(SELECT COALESCE(SUM(tokens_out),0) FROM label WHERE run_id=label_run.id)),legacy_cost_usd=cost_usd,usage_complete=0;
	 UPDATE label_run SET tokens_in=legacy_tokens_in,tokens_out=legacy_tokens_out,
	 total=MAX(total,(SELECT COUNT(*) FROM label WHERE run_id=label_run.id)),
	 ok=(SELECT COUNT(*) FROM label WHERE run_id=label_run.id),failed=MAX(total-(SELECT COUNT(*) FROM label WHERE run_id=label_run.id),0);
	 UPDATE label_run SET status=CASE WHEN status='running' THEN status WHEN total=ok THEN 'finished' WHEN ok>0 THEN 'partial' ELSE 'failed' END;
	 CREATE TABLE label_call(
	 id INTEGER PRIMARY KEY,run_id TEXT NOT NULL REFERENCES label_run(id),question_id INTEGER NOT NULL REFERENCES question(id),
	 model_config TEXT NOT NULL,model_response TEXT,tokens_in INTEGER NOT NULL,tokens_out INTEGER NOT NULL,cost_usd REAL,usage_known INTEGER NOT NULL,
	 status TEXT NOT NULL,created_at TEXT NOT NULL);
	 CREATE INDEX idx_call_run ON label_call(run_id);`,
	// v11：多端草稿乐观锁，旧练习从 revision=0 开始。
	`ALTER TABLE practice_session ADD COLUMN draft_revision INTEGER NOT NULL DEFAULT 0;`,
	// v12：个人资料与公开会话管理；旧凭据和草稿保持原样。
	`CREATE TABLE user_profile (
 user_id INTEGER PRIMARY KEY REFERENCES app_user(id) ON DELETE CASCADE,
 bio TEXT NOT NULL DEFAULT '', avatar_id INTEGER NOT NULL DEFAULT 0,
 daily_questions INTEGER NOT NULL DEFAULT 20, daily_minutes INTEGER NOT NULL DEFAULT 30,
 exam_name TEXT NOT NULL DEFAULT '', exam_date TEXT NOT NULL DEFAULT '',
 default_limit INTEGER NOT NULL DEFAULT 20, default_module TEXT NOT NULL DEFAULT '',
 reading_size INTEGER NOT NULL DEFAULT 16, revision INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL
);
INSERT INTO user_profile(user_id,updated_at) SELECT id,created_at FROM app_user;
ALTER TABLE app_session ADD COLUMN public_id TEXT;
ALTER TABLE app_session ADD COLUMN created_at TEXT;
ALTER TABLE app_session ADD COLUMN device_label TEXT;
ALTER TABLE app_session ADD COLUMN ip_hint TEXT;
UPDATE app_session SET public_id=lower(hex(randomblob(16)));
CREATE UNIQUE INDEX idx_app_session_public ON app_session(public_id);
CREATE INDEX idx_app_session_user ON app_session(user_id,expires_at);
CREATE INDEX idx_practice_user_submitted ON practice_session(user_id,submitted_at);`,
}

// Open 打开（必要时创建）数据库并跑迁移。
func Open(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)&_txlock=immediate", url.PathEscape(path))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc SQLite 在 WAL 模式下允许多个读连接，但写操作仍需串行获取锁。
	// 写事务使用 BEGIN IMMEDIATE，在读快照前获取写锁以避免 BUSY_SNAPSHOT。
	// 分析读取需 BeginTx(ctx, &sql.TxOptions{ReadOnly:true})，驱动使用普通 BEGIN，
	// 避免读取分析持有写锁；普通 Query 也可并发读取。
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(5)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migration (
		version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	var cur int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migration`).Scan(&cur); err != nil {
		return err
	}
	for i := cur; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("迁移 v%d 失败: %w", i+1, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migration(version, applied_at) VALUES (?, datetime('now'))`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
