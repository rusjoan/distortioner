create table events (
    id          integer primary key,
    chat_id     integer not null,
    is_group    integer not null,
    kind        text    not null,
    status      text    not null,
    duration_ms integer not null default 0,
    created_at  integer not null
);
create index events_created_at on events (created_at);

create table chat_settings (
    chat_id       integer primary key,
    strength      integer not null,
    distort_audio integer not null,
    lang          text    not null default ''
);

create table bans (
    chat_id    integer primary key,
    created_at integer not null
);

create table priority_chats (
    chat_id    integer primary key,
    created_at integer not null
);

create table settings (
    key   text primary key,
    value text not null
);

create table failures (
    id         integer primary key,
    chat_id    integer not null,
    kind       text    not null,
    error      text    not null,
    input_path text    not null default '',
    created_at integer not null
);
create index failures_created_at on failures (created_at);
