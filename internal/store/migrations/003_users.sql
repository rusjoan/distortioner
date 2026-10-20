alter table events add column user_id integer not null default 0;
-- Older events have no user; in a private chat the chat id is the user id.
update events set user_id = chat_id where chat_id > 0;
