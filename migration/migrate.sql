-- public.carriers definition

-- Drop table

-- DROP TABLE public.carriers;

CREATE TABLE public.carriers (
	id bigserial NOT NULL,
	"name" text NOT NULL,
	"type" text NOT NULL,
	username text NOT NULL,
	"password" text NOT NULL,
	"uuid" text NOT NULL,
	CONSTRAINT carriers_pkey PRIMARY KEY (id),
	CONSTRAINT uni_carriers_name UNIQUE (name),
	CONSTRAINT uni_carriers_uuid UNIQUE (uuid)
);

-- public.client_numbers definition

-- Drop table

-- DROP TABLE public.client_numbers;

CREATE TABLE public.client_numbers (
	id bigserial NOT NULL,
	client_id int8 NOT NULL,
	"number" text NOT NULL,
	carrier text NULL,
	web_hook text NULL,
	ignore_stop_cmd_sending bool DEFAULT false NOT NULL,
	CONSTRAINT client_numbers_pkey PRIMARY KEY (id),
	CONSTRAINT uni_client_numbers_number UNIQUE (number)
);
CREATE INDEX idx_client_numbers_client_id ON public.client_numbers USING btree (client_id);


-- public.client_numbers foreign keys

ALTER TABLE public.client_numbers ADD CONSTRAINT fk_clients_numbers FOREIGN KEY (client_id) REFERENCES public.clients(id);

-- public.clients definition

-- Drop table

-- DROP TABLE public.clients;

CREATE TABLE public.clients (
	id bigserial NOT NULL,
	username text NOT NULL,
	"password" text NOT NULL,
	address text NULL,
	"name" text NULL,
	log_privacy bool NULL,
	CONSTRAINT clients_pkey PRIMARY KEY (id),
	CONSTRAINT uni_clients_username UNIQUE (username)
);

-- public.media_files definition

-- Drop table

-- DROP TABLE public.media_files;

CREATE TABLE public.media_files (
	id bigserial NOT NULL,
	file_name text NULL,
	content_type text NULL,
	base64_data text NULL,
	upload_at timestamptz NULL,
	expires_at timestamptz NULL,
	CONSTRAINT media_files_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_media_files_expires_at ON public.media_files USING btree (expires_at);

-- public.msg_record_db_items definition

-- Drop table

-- DROP TABLE public.msg_record_db_items;

CREATE TABLE public.msg_record_db_items (
	id bigserial NOT NULL,
	client_id int8 NOT NULL,
	"to" text NULL,
	"from" text NULL,
	received_timestamp timestamptz NULL,
	"type" text NULL,
	redacted_message text NULL,
	carrier text NULL,
	internal bool NULL,
	log_id text NULL,
	server_id text NULL,
	msg_data text NULL,
	CONSTRAINT msg_record_db_items_pkey PRIMARY KEY (id)
);
CREATE INDEX idx_msg_record_db_items_client_id ON public.msg_record_db_items USING btree (client_id);