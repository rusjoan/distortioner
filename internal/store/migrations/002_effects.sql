alter table chat_settings add column fry_strength integer not null default 50;

alter table events add column source text not null default '';

create table effect_uses (
    event_id integer not null references events (id) on delete cascade,
    effect   text    not null
);
create index effect_uses_event on effect_uses (event_id);
