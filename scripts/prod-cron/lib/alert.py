#!/usr/bin/env python3
"""Send one maintenance alert over the site's own SMTP account.

Stdlib only, on purpose: the box has no msmtp/sendmail/mail, and an alert
path that needs its own package to be installed is one more thing that can
be missing on the day it finally matters.

Credentials arrive as a KEY=VALUE file (the oauth container's env, dumped by
the caller and shredded by it) rather than as arguments or exported shell
variables: argv is world-readable via /proc, and sourcing a secrets file
leaks it into every child process. Nothing here ever prints a value.
"""
import ssl
import sys
import smtplib
from email.message import EmailMessage

PREFIX = "KUN_VISUAL_NOVEL_EMAIL_"


def main():
    if len(sys.argv) != 5:
        sys.exit("usage: alert.py <env-file> <subject> <body-file|-> <to>")
    env_path, subject, body_path, to = sys.argv[1:5]

    cfg = {}
    with open(env_path, encoding="utf-8") as fh:
        for line in fh:
            key, _, value = line.rstrip("\n").partition("=")
            if key.startswith(PREFIX):
                cfg[key[len(PREFIX):]] = value

    missing = [k for k in ("HOST", "PORT", "ACCOUNT", "PASSWORD") if not cfg.get(k)]
    if missing:
        sys.exit("alert: SMTP config incomplete, missing %s" % ",".join(missing))

    if body_path == "-":
        body = sys.stdin.read()
    else:
        with open(body_path, encoding="utf-8", errors="replace") as fh:
            body = fh.read()

    # Keep the mail small enough to actually be delivered and read on a phone:
    # the tail is where a failing run says why it died.
    lines = body.splitlines()
    if len(lines) > 200:
        body = "[... %d earlier lines omitted ...]\n" % (len(lines) - 200) + "\n".join(lines[-200:])

    msg = EmailMessage()
    msg["From"] = "%s <%s>" % (cfg.get("FROM", "KUN infra"), cfg["ACCOUNT"])
    msg["To"] = to
    msg["Subject"] = subject
    msg.set_content(body)

    try:
        with smtplib.SMTP(cfg["HOST"], int(cfg["PORT"]), timeout=30) as smtp:
            smtp.starttls(context=ssl.create_default_context())
            smtp.login(cfg["ACCOUNT"], cfg["PASSWORD"])
            smtp.send_message(msg)
    except Exception as exc:  # noqa: BLE001 - the class and message are the whole diagnosis
        # Deliberately not re-raising: a traceback would print the smtplib call
        # frame, and the caller only needs to know delivery did not happen.
        sys.exit("alert: send failed: %s: %s" % (type(exc).__name__, exc))

    print("alert sent to %s" % to)


if __name__ == "__main__":
    main()
