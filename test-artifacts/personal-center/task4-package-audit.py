"""Read-only local release audit; writes a manifest, never extracts or opens a DB."""
import hashlib
import json
import tarfile
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[2]
archive = root / "var/gk-linux-amd64.tar.gz"
allowed_roots = {"README.md", "第一轮修复文档.md", "第二轮修复文档.md", "第二轮上线就绪审查报告.md", "gk", "deploy", "taxonomy", "docs"}
required = {"gk", "docs/personal-center.md", "docs/personal-center-task4-report.md", "docs/api-design.md", "docs/deployment-2c4g.md", "README.md"}
final_fix = '--final-fix' in sys.argv
if final_fix:
    required.add('docs/personal-center-final-fix-report.md')
files = {}
with tarfile.open(archive, "r:gz") as tar:
    for member in tar.getmembers():
        path = Path(member.name)
        assert not path.is_absolute() and ".." not in path.parts, member.name
        assert path.parts[0] in allowed_roots, member.name
        assert not ({"test-artifacts", "_work", "backups", "__pycache__", "tests"} & set(path.parts)), member.name
        assert not member.name.endswith((".sqlite", ".sqlite-wal", ".sqlite-shm", ".db", ".key", ".env", ".py", "_test.go")), member.name
        assert member.isfile() or member.isdir(), member.name
        if member.isfile():
            payload = tar.extractfile(member).read()
            if member.name == "gk":
                assert payload.startswith(b"\x7fELF"), "not a Linux ELF"
                assert "个人中心".encode() in payload, "personal-center frontend not embedded"
                if final_fix:
                    assert b'X-GK-Expected-User' in payload and b'account_changed' in payload, 'identity precondition/frontend absent'
                expected = (root / "var/release/gk").read_bytes()
            else:
                expected = (root / member.name).read_bytes()
            assert payload == expected, "package differs from current workspace: " + member.name
            files[member.name] = {"size": len(payload), "sha256": hashlib.sha256(payload).hexdigest()}
assert required <= files.keys(), required - files.keys()
result = {"archive": str(archive.relative_to(root)), "size": archive.stat().st_size, "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(), "file_count": len(files), "files": files, "checks": {"allowed_members_only": True, "no_database_key_backup_or_fixture": True, "documents_match_workspace": True, "linux_elf_with_personal_frontend": True}, "note": "deploy/backup.sh and backup systemd templates are source configuration, not backup data."}
destination = root / ("test-artifacts/personal-center/final-fix-package-manifest.json" if final_fix else "test-artifacts/personal-center/task4-package-manifest.json")
destination.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n")
print(json.dumps({k: v for k, v in result.items() if k != "files"}, ensure_ascii=False, indent=2))
