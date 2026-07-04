"""Windows desktop toast notifications for anomaly alerts."""

try:
    from win11toast import notify as _win_notify
    _HAS_TOAST = True
except ImportError:
    _HAS_TOAST = False


def alert(title, message):
    if _HAS_TOAST:
        try:
            _win_notify(title, message)
            return
        except Exception as e:
            print(f"[notifier] toast failed ({e}), falling back to console")
    print(f"[ALERT] {title}: {message}")
