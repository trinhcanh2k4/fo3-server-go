import subprocess, sys, os

snap, uid = sys.argv[1], sys.argv[2]
pw = subprocess.run(["bash", "-c", 'sed -n "s/.*fo3app:\\([^@]*\\)@tcp.*/\\1/p" /etc/fo3/db.env'],
                    capture_output=True, text=True).stdout.strip()
doc = None
for line in open(snap):
    parts = line.rstrip("\n").split("\t")
    if len(parts) == 3 and parts[0] == uid and parts[1] == "core":
        doc = parts[2]
        break
assert doc, "snapshot row not found"
esc = doc.replace("\\", "\\\\").replace("'", "\\'")
with open("/tmp/restore.sql", "w") as f:
    f.write("UPDATE user_state SET doc = '%s' WHERE user_id = %s AND domain = 'core';\n" % (esc, uid))
env = dict(os.environ, MYSQL_PWD=pw)
r = subprocess.run(["mysql", "-u", "fo3app", "fo3", "-e", "source /tmp/restore.sql"],
                   env=env, capture_output=True, text=True)
print("restore rc:", r.returncode, r.stderr[:120])
