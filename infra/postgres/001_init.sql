create table if not exists products (
  id text primary key,
  manifest jsonb not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create table if not exists builds (
  id text primary key,
  product_id text not null,
  target text not null,
  profile text,
  customer text,
  version text,
  release_channel text,
  status text not null,
  attempt integer not null default 0,
  runner_id text,
  nomad_job_id text,
  artifact_uri text,
  download_url text,
  error text,
  payload jsonb not null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now()
);

create index if not exists builds_status_target_idx on builds(status, target);
create index if not exists builds_created_at_idx on builds(created_at desc);

create table if not exists uploads (
  id text primary key,
  file_name text not null,
  uri text not null,
  download_url text not null,
  sha256 text not null,
  size_bytes bigint not null,
  payload jsonb not null,
  created_at timestamptz not null default now()
);
