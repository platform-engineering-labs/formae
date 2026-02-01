-- +goose Up
CREATE TABLE IF NOT EXISTS stacks (
    id TEXT NOT NULL,
    version TEXT NOT NULL,
    valid_from TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    command_id TEXT,
    operation TEXT NOT NULL,
    label TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (id, version)
);

CREATE INDEX IF NOT EXISTS idx_stacks_label ON stacks (label);
CREATE INDEX IF NOT EXISTS idx_stacks_valid_from ON stacks (valid_from);
CREATE INDEX IF NOT EXISTS idx_stacks_command_id ON stacks (command_id);

-- KSUID generation function for PostgreSQL
-- Based on https://gist.github.com/fabiolimace/5e7923803566beefaf3c716d1343ae27
CREATE OR REPLACE FUNCTION generate_ksuid() RETURNS TEXT AS $$
DECLARE
    v_time TIMESTAMP WITH TIME ZONE := NULL;
    v_seconds NUMERIC(50) := NULL;
    v_numeric NUMERIC(50) := NULL;
    v_epoch NUMERIC(50) = 1400000000; -- 2014-05-13T16:53:20Z
    v_base62 TEXT := '';
    v_alphabet CHAR ARRAY[62] := ARRAY[
        '0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
        'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J',
        'K', 'L', 'M', 'N', 'O', 'P', 'Q', 'R', 'S', 'T',
        'U', 'V', 'W', 'X', 'Y', 'Z',
        'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j',
        'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't',
        'u', 'v', 'w', 'x', 'y', 'z'];
    i INTEGER := 0;
BEGIN
    v_time := clock_timestamp();
    v_seconds := FLOOR(EXTRACT(EPOCH FROM v_time)) - v_epoch;
    v_numeric := v_seconds * pow(2::NUMERIC(50), 128)
        + ((random()::NUMERIC(70,20) * pow(2::NUMERIC(70,20), 48))::NUMERIC(50) * pow(2::NUMERIC(50), 80)::NUMERIC(50))
        + ((random()::NUMERIC(70,20) * pow(2::NUMERIC(70,20), 40))::NUMERIC(50) * pow(2::NUMERIC(50), 40)::NUMERIC(50))
        + (random()::NUMERIC(70,20) * pow(2::NUMERIC(70,20), 40))::NUMERIC(50);

    WHILE v_numeric <> 0 LOOP
        v_base62 := v_base62 || v_alphabet[mod(v_numeric, 62) + 1];
        v_numeric := div(v_numeric, 62);
    END LOOP;

    v_base62 := reverse(v_base62);
    v_base62 := lpad(v_base62, 27, '0');
    RETURN v_base62;
END $$ LANGUAGE plpgsql;

-- Seed with existing stack labels from resources (excluding $unmanaged stack)
INSERT INTO stacks (id, version, operation, label, description)
SELECT 
    generate_ksuid(),
    generate_ksuid(),
    'create',
    stack,
    ''
FROM (
    SELECT DISTINCT stack
    FROM resources
    WHERE stack IS NOT NULL
      AND stack != ''
      AND stack != '$unmanaged'
) AS distinct_stacks;

-- Drop the helper function after seeding
DROP FUNCTION IF EXISTS generate_ksuid();

-- +goose Down
DROP INDEX IF EXISTS idx_stacks_command_id;
DROP INDEX IF EXISTS idx_stacks_valid_from;
DROP INDEX IF EXISTS idx_stacks_label;
DROP TABLE IF EXISTS stacks;
