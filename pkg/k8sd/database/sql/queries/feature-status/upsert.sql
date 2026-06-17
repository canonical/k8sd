INSERT INTO
    feature_status(name, component, message, version, timestamp, enabled)
VALUES
    (?, ?, ?, ?, ?, ?)
ON CONFLICT(name) DO UPDATE SET
    message=excluded.message,
    component=excluded.component,
    version=excluded.version,
    timestamp=excluded.timestamp,
    enabled=excluded.enabled;
