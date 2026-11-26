-- 保留升级前的实例和默认配置，便于管理员核对远端数据卷与凭据引用。
CREATE TABLE IF NOT EXISTS retired_environment_state (
    id TEXT PRIMARY KEY,
    installations TEXT NOT NULL,
    defaults TEXT NOT NULL,
    catalog TEXT NOT NULL
);

INSERT INTO retired_environment_state (id, installations, defaults, catalog)
SELECT id, COALESCE(json_extract(state, '$.environmentInstallations'), '[]'),
       COALESCE(json_extract(state, '$.environmentDefaultConfigs'), '[]'),
       COALESCE(json_extract(state, '$.environmentCatalog'), '[]')
FROM app_state
WHERE json_array_length(state, '$.environmentInstallations') > 0
   OR json_array_length(state, '$.environmentDefaultConfigs') > 0;
