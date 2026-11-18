create table if not exists posts (
  slug        text primary key check (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' and length(slug) <= 80),
  title       text not null,
  body        text not null,
  updated_at  timestamptz not null default now(),
  deleted_at  timestamptz            -- set instead of deleting the row: the page answers 410
);

create table if not exists slug_redirects (
  old_slug text primary key,
  new_slug text not null references posts(slug)
);

-- keep updated_at honest so the version token changes on every edit
create or replace function touch_updated_at() returns trigger as $$
begin new.updated_at = now(); return new; end $$ language plpgsql;
drop trigger if exists posts_touch on posts;
create trigger posts_touch before update on posts for each row execute function touch_updated_at();

insert into posts (slug, title, body) values
  ('hello-world', 'Hello, world', 'This page was rendered from Postgres once, written to a file, and every later request is served from that file.'),
  ('static-speed', 'Static speed, dynamic content', 'Update a row and the next visit renders it again.')
on conflict do nothing;
insert into posts (slug, title, body, deleted_at) values ('retired-post', 'Retired', 'gone', now()) on conflict do nothing;
insert into slug_redirects (old_slug, new_slug) values ('hello', 'hello-world') on conflict do nothing;
