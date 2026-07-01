-- Re-seed org and admin user after docker compose down -v
-- Password: Admin1234!

INSERT INTO orgs (name, slug, plan)
VALUES ('Avula', 'avula', 'trial')
ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name;

DO $$
DECLARE
    v_org_id UUID;
BEGIN
    SELECT id INTO v_org_id FROM orgs WHERE slug = 'avula';

    INSERT INTO users (org_id, email, name, role, password_hash)
    VALUES (
        v_org_id,
        'bhargavrash@gmail.com',
        'Bhargav',
        'admin',
        '$2b$10$akdPKywF9r6g9sqz7r8aYumO.h.iorcNlqfbWq8mangklGJTa03PK'
    )
    ON CONFLICT (email) DO UPDATE
        SET name          = EXCLUDED.name,
            role          = 'admin',
            password_hash = EXCLUDED.password_hash,
            updated_at    = NOW();
END;
$$;

SELECT u.email, u.role, o.name AS org
FROM users u JOIN orgs o ON o.id = u.org_id;
