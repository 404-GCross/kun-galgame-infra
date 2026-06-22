#!/bin/sh
# Runs once, on first Postgres init (empty data dir). The entrypoint already
# created POSTGRES_DB (kun_galgame_infra); create the other databases the
# services connect to. Schema itself is built by the migrate jobs, not here.
set -e

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-'EOSQL'
	CREATE DATABASE kun_galgame_wiki;
	CREATE DATABASE kun_images;
	CREATE DATABASE kun_artifacts;
	-- Downstream repos' databases live on this shared server too (the hub owns
	-- the single Postgres). kungal → kungalgame, moyu → kungalgame_patch,
	-- sticker → kungalgame_sticker.
	CREATE DATABASE kungalgame;
	CREATE DATABASE kungalgame_patch;
	CREATE DATABASE kungalgame_sticker;
EOSQL
