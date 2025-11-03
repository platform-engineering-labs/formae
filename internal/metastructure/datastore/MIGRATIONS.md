# Database Migrations

We use [goose](https://github.com/pressly/goose) for database migrations. Migrations run automatically on agent startup.

## DB Flavors - Directory Structure

```
migrations_sqlite/
migrations_postgres/
```

Separate directories handle dialect differences (e.g. INTEGER vs BOOLEAN, TEXT vs JSONB)

## Adding a Migration

1. Create sequentially numbered migration files in **both** directories:
   ```
   migrations_sqlite/0000N_description.sql
   migrations_postgres/0000N_description.sql
   ```

   Or use goose to generate the next number:
   ```bash
   cd internal/metastructure/datastore/migrations_sqlite
   goose create description sql
   ```

2. Use goose syntax with Up/Down sections:
   ```sql
   -- +goose Up
   ALTER TABLE targets ADD COLUMN created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;

   -- +goose Down
   ALTER TABLE targets DROP COLUMN created_at;
   ```

3. **Important dialect differences:**
   - SQLite: `DROP COLUMN` not supported (requires table recreation), use `INTEGER` for booleans
   - PostgreSQL: Full DDL support, use `BOOLEAN` type, supports `IF NOT EXISTS` clauses

4. Migrations are embedded at compile time via `//go:embed` and run automatically on agent start up

## Testing

Migrations run in all datastore tests:
```bash
go test -tags=unit ./internal/metastructure/datastore
```

## Migration State

Goose tracks migrations in a `db_version` table. Version is stored per migration file
