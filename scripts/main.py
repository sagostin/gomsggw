#!/usr/bin/env python3
"""
GOMSGGW Client Manager - CLI tool for managing SMS/MMS gateway clients and carriers.
"""
import os
import sys
import json
import getpass
import re
import secrets
import string
from typing import List, Optional, Tuple, Dict, Any

import requests

DEFAULT_BASE_URL = os.getenv("MSGGW_BASE_URL", "http://API_URL")
API_KEY = os.getenv("MSGGW_API_KEY", "API_KEY")

TIMEOUT = 15
NUM_RE = re.compile(r"^\s*\d{10,11}\s*$")


def generate_password(length: int = 24) -> str:
    """Generate a strong random password."""
    alphabet = string.ascii_letters + string.digits
    while True:
        pwd = "".join(secrets.choice(alphabet) for _ in range(length))
        if (
            any(c.islower() for c in pwd)
            and any(c.isupper() for c in pwd)
            and any(c.isdigit() for c in pwd)
        ):
            return pwd


def auth_tuple() -> Tuple[str, str]:
    key = API_KEY
    if not key or key == "API_KEY":
        print("No MSGGW_API_KEY in environment. Enter it now.")
        key = getpass.getpass("API key: ").strip()
        if not key:
            print("Error: API key required.", file=sys.stderr)
            sys.exit(1)
    return ("apikey", key)


def base_url() -> str:
    return os.getenv("MSGGW_BASE_URL", DEFAULT_BASE_URL).rstrip("/")


def get_json(path: str) -> requests.Response:
    url = f"{base_url()}{path}"
    return requests.get(url, auth=auth_tuple(), timeout=TIMEOUT)


def post_json(path: str, payload: dict) -> requests.Response:
    url = f"{base_url()}{path}"
    return requests.post(
        url,
        auth=auth_tuple(),
        headers={"Content-Type": "application/json"},
        data=json.dumps(payload),
        timeout=TIMEOUT,
    )


def put_json(path: str, payload: dict) -> requests.Response:
    url = f"{base_url()}{path}"
    return requests.put(
        url,
        auth=auth_tuple(),
        headers={"Content-Type": "application/json"},
        data=json.dumps(payload),
        timeout=TIMEOUT,
    )


# =============================================================================
# Carrier Operations
# =============================================================================

def list_carriers() -> Optional[List[Dict[str, Any]]]:
    """List all carriers from the gateway."""
    print("\n=== All Carriers ===")
    try:
        resp = get_json("/carriers")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code != 200:
        print(f"❌ Failed to list carriers ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)
        return None

    carriers = resp.json()
    if not carriers:
        print("No carriers found.")
        return []

    print(f"\n{'Name':<20} {'Type':<12} {'Active':<8} {'SMS Limit':<12} {'MMS Limit':<12}")
    print("-" * 70)
    for c in carriers:
        name = c.get("name", "")[:20]
        ctype = c.get("type", "")[:12]
        active = "✅" if c.get("active", True) else "❌"
        sms_limit = c.get("sms_limit", 0)
        mms_limit = c.get("mms_limit", 0)
        sms_str = f"{sms_limit:,}" if sms_limit > 0 else "unlimited"
        mms_str = f"{mms_limit:,}" if mms_limit > 0 else "unlimited"
        print(f"{name:<20} {ctype:<12} {active:<8} {sms_str:<12} {mms_str:<12}")

    print(f"\nTotal: {len(carriers)} carriers")
    return carriers


def create_carrier_interactive() -> Optional[str]:
    """Interactively create a new carrier."""
    print("\n=== Create New Carrier ===")
    
    name = input("Carrier Name (e.g., telnyx_prod): ").strip()
    if not name:
        print("Name is required.")
        return None

    print("\nCarrier Type:")
    print("  1) telnyx")
    print("  2) twilio")
    print("  3) bandwidth")
    print("  4) plivo")
    type_choice = input("Choose [1-4, default=1]: ").strip()
    carrier_types = {"1": "telnyx", "2": "twilio", "3": "bandwidth", "4": "plivo"}
    carrier_type = carrier_types.get(type_choice, "telnyx")

    print(f"\nEnter {carrier_type.upper()} credentials:")
    if carrier_type == "telnyx":
        username = input("API Key: ").strip()
        password = input("API Secret (or leave blank): ").strip() or ""
    elif carrier_type == "twilio":
        username = input("Account SID: ").strip()
        password = getpass.getpass("Auth Token: ").strip()
    else:
        username = input("Username/API Key: ").strip()
        password = getpass.getpass("Password/Secret: ").strip()

    # Limits
    sms_limit_input = input("SMS Size Limit bytes (default: 600000): ").strip()
    try:
        sms_limit = int(sms_limit_input) if sms_limit_input else 600000
    except ValueError:
        sms_limit = 600000

    mms_limit_input = input("MMS Size Limit bytes (default: 1048576): ").strip()
    try:
        mms_limit = int(mms_limit_input) if mms_limit_input else 1048576
    except ValueError:
        mms_limit = 1048576

    payload = {
        "name": name,
        "type": carrier_type,
        "username": username,
        "password": password,
        "sms_limit": sms_limit,
        "mms_limit": mms_limit,
    }

    try:
        resp = post_json("/carriers", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if 200 <= resp.status_code < 300:
        print(f"✅ Carrier created: {name} ({carrier_type})")
        return name
    else:
        print(f"❌ Failed to create carrier ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)
        return None


def reload_carriers() -> None:
    """Trigger carrier reload on the gateway."""
    print("\n=== Reload Carriers ===")
    try:
        resp = post_json("/carriers/reload", {})
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if 200 <= resp.status_code < 300:
        print("✅ Carriers reloaded.")
    else:
        print(f"❌ Reload failed ({resp.status_code})")


# =============================================================================
# Client Operations
# =============================================================================

def list_clients() -> Optional[List[Dict[str, Any]]]:
    """List all clients from the gateway."""
    print("\n=== All Clients ===")
    try:
        resp = get_json("/clients")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code != 200:
        print(f"❌ Failed to list clients ({resp.status_code})")
        return None

    clients = resp.json()
    if not clients:
        print("No clients found.")
        return []

    print(f"\n{'ID':<6} {'Username':<18} {'Name':<22} {'Type':<8} {'Limit':<10} {'Nums':<6}")
    print("-" * 75)
    for c in clients:
        cid = str(c.get("id", ""))[:6]
        username = c.get("username", "")[:18]
        name = (c.get("name") or "")[:22]
        ctype = c.get("type", "legacy")[:8]
        limit = c.get("sms_limit", 0)
        limit_str = str(limit) if limit > 0 else "∞"
        num_count = len(c.get("numbers") or [])
        print(f"{cid:<6} {username:<18} {name:<22} {ctype:<8} {limit_str:<10} {num_count:<6}")

    print(f"\nTotal: {len(clients)} clients")
    return clients


def get_client_by_identifier(identifier: str) -> Optional[Dict[str, Any]]:
    """Get client by ID or username."""
    try:
        resp = get_json("/clients")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code != 200:
        return None

    clients = resp.json()
    # Try ID first
    if identifier.isdigit():
        for c in clients:
            if c.get("id") == int(identifier):
                return c
    # Then try username
    for c in clients:
        if c.get("username") == identifier:
            return c
    return None


def get_client(username: str) -> Optional[Dict[str, Any]]:
    """Get details for a specific client by username (legacy)."""
    return get_client_by_identifier(username)


def show_client_details(identifier: str) -> None:
    """Show detailed info for a client (by ID or username)."""
    print(f"\n=== Client Details ===")
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return

    print(f"  ID: {client.get('id')}")
    print(f"  Username: {client.get('username')}")
    print(f"  Name: {client.get('name', 'N/A')}")
    print(f"  Type: {client.get('type', 'legacy')}")
    print(f"  Address: {client.get('address', 'N/A')}")
    print(f"  SMS Limit: {client.get('sms_limit', 0) or 'unlimited'}")

    # Web settings
    ws = client.get("web_settings")
    if ws:
        print(f"\n  Web Settings:")
        print(f"    API Format: {ws.get('api_format', 'generic')}")
        print(f"    Default Webhook: {ws.get('default_webhook', 'N/A')}")
        print(f"    Webhook Retries: {ws.get('webhook_retries', 3)}")
        print(f"    Timeout: {ws.get('webhook_timeout_secs', 10)}s")

    # Numbers
    numbers = client.get("numbers") or []
    if numbers:
        print(f"\n  Numbers ({len(numbers)}):")
        for n in numbers:
            num = n.get("number", "")
            carrier = n.get("carrier", "")
            tag = n.get("tag", "")
            limit = n.get("sms_limit", 0)
            limit_str = f" (limit: {limit})" if limit > 0 else ""
            tag_str = f" [{tag}]" if tag else ""
            print(f"    - {num} via {carrier}{tag_str}{limit_str}")
    else:
        print("\n  No numbers configured.")


def create_client_interactive() -> Optional[str]:
    """Interactively create a new client."""
    print("\n=== Create New Client ===")
    username = input("Username (e.g., tops_zultys): ").strip()
    if not username:
        print("Username is required.")
        return None

    password = getpass.getpass("Password (leave blank to auto-generate): ").strip()
    if not password:
        password = generate_password()
        print("\n🔑 Generated password (save this now; it will not be shown again):")
        print(f"  {password}\n")

    name = input("Display Name (company name): ").strip()

    # Client type
    print("\nClient Type:")
    print("  1) legacy (SMPP/MM4 - for Zultys, etc.)")
    print("  2) web (REST API/Webhooks - for Bicom, web apps)")
    type_choice = input("Choose [1/2, default=1]: ").strip()
    client_type = "web" if type_choice == "2" else "legacy"

    # Address (required for legacy, optional for web)
    if client_type == "legacy":
        address = input("Address (IP or hostname, REQUIRED for legacy): ").strip()
        if not address:
            print("❌ Address is required for legacy clients (used for SMPP ACL and MM4 delivery)")
            return None
    else:
        address = input("Address (IP or hostname, optional): ").strip()

    # SMS limit
    limit_input = input("Daily SMS Limit (0 = unlimited): ").strip()
    try:
        sms_limit = int(limit_input) if limit_input else 0
    except ValueError:
        sms_limit = 0

    payload = {
        "username": username,
        "password": password,
        "name": name,
        "type": client_type,
        "sms_limit": sms_limit,
    }
    if address:
        payload["address"] = address

    try:
        resp = post_json("/clients", payload)
    except requests.RequestException as e:
        print(f"Network error creating client: {e}")
        return None

    if 200 <= resp.status_code < 300:
        data = resp.json()
        client_id = data.get("id", "?")
        print(f"✅ Client created: {username} (ID: {client_id}, Type: {client_type})")
        return str(client_id)  # Return ID instead of username
    else:
        print(f"❌ Failed to create client ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)
        return None


def update_client_settings(identifier: str) -> None:
    """Update web client settings (by ID or username)."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return
    
    client_id = client.get("id")
    print(f"\n=== Update Settings for '{client.get('username')}' (ID: {client_id}) ===")

    settings = {}
    
    print("\nAPI Format:")
    print("  1) generic (default)")
    print("  2) bicom (Bicom PBXware)")
    print("  3) telnyx")
    format_choice = input("Choose [1-3, leave blank to skip]: ").strip()
    formats = {"1": "generic", "2": "bicom", "3": "telnyx"}
    if format_choice in formats:
        settings["api_format"] = formats[format_choice]

    webhook = input("Default Webhook URL (leave blank to skip): ").strip()
    if webhook:
        settings["default_webhook"] = webhook

    retries = input("Webhook Retries (leave blank to skip): ").strip()
    if retries:
        try:
            settings["webhook_retries"] = int(retries)
        except ValueError:
            pass

    timeout = input("Webhook Timeout Seconds (leave blank to skip): ").strip()
    if timeout:
        try:
            settings["webhook_timeout_secs"] = int(timeout)
        except ValueError:
            pass

    if not settings:
        print("No settings to update.")
        return

    try:
        resp = put_json(f"/clients/{client_id}/settings", settings)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if 200 <= resp.status_code < 300:
        print("✅ Settings updated.")
    else:
        print(f"❌ Failed ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)


# =============================================================================
# Number Operations
# =============================================================================

def get_client_numbers(username: str) -> List[str]:
    """Get list of numbers already assigned to a client."""
    client = get_client(username)
    if not client:
        return []
    return [n.get("number", "") for n in (client.get("numbers") or [])]


def normalize_number(num: str) -> str:
    """Normalize a phone number to digits only."""
    return re.sub(r"[^\d]", "", num)


def parse_numbers_csv(raw: str) -> List[str]:
    """Parse comma-separated numbers, validating format.
    
    Accepts formats:
      - 10-digit:  2505551234      → 12505551234
      - 11-digit:  12505551234     → 12505551234
      - E.164:     +12505551234    → 12505551234
      - With dashes/spaces: +1-250-555-1234 → 12505551234
    """
    parts = [p.strip() for p in raw.replace("\n", ",").split(",") if p.strip()]
    valid, invalid = [], []
    for p in parts:
        normalized = normalize_number(p)  # strips to digits only
        if len(normalized) >= 10 and len(normalized) <= 15:
            # E.164 can be up to 15 digits; we want NANP 11-digit
            if len(normalized) == 10:
                normalized = "1" + normalized
            elif len(normalized) > 11:
                # Likely E.164 with country code; take last 11 for NANP
                # e.g. digits "12505551234" from "+12505551234" is already 11
                # but "112505551234" (extra leading 1) → take last 11
                normalized = normalized[-11:]
            valid.append(normalized)
            if normalized != normalize_number(p):
                print(f"  ℹ️  {p} → {normalized}")
        else:
            invalid.append(p)
    if invalid:
        print("⚠️ These entries are invalid:")
        for bad in invalid:
            print(f"  - {bad}")
    return valid


def add_numbers_to_client(
    identifier: str, numbers: List[str], carrier: str = "telnyx", skip_existing: bool = True
) -> None:
    """Add numbers to a client (by ID or username), optionally skipping existing ones."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return
    
    client_id = client.get("id")
    print(f"\n=== Add Numbers to '{client.get('username')}' (ID: {client_id}) ===")

    existing = []
    if skip_existing:
        existing = [n.get("number", "") for n in (client.get("numbers") or [])]
        print(f"  Client has {len(existing)} existing numbers.")

    added, skipped, failed = 0, 0, 0
    for num in numbers:
        if num in existing:
            print(f"  {num}: ⏭️ already exists, skipping")
            skipped += 1
            continue

        payload = {"number": num, "carrier": carrier}
        try:
            resp = post_json(f"/clients/{client_id}/numbers", payload)
        except requests.RequestException as e:
            print(f"  {num}: ❌ network error: {e}")
            failed += 1
            continue

        if 200 <= resp.status_code < 300:
            print(f"  {num}: ✅ added")
            added += 1
            existing.append(num)  # Track for duplicates in same batch
        else:
            try:
                body = resp.json()
                err = body.get("error", str(body))
            except Exception:
                err = resp.text
            if "already exists" in str(err).lower():
                print(f"  {num}: ⏭️ already exists")
                skipped += 1
            else:
                print(f"  {num}: ❌ failed ({resp.status_code}) -> {err}")
                failed += 1

    print(f"\nDone: {added} added, {skipped} skipped, {failed} failed")


def list_client_numbers(username: str) -> None:
    """List all numbers for a client."""
    print(f"\n=== Numbers for '{username}' ===")
    client = get_client(username)
    if not client:
        print(f"Client '{username}' not found.")
        return

    numbers = client.get("numbers") or []
    if not numbers:
        print("No numbers configured.")
        return

    print(f"\n{'Number':<15} {'Carrier':<12} {'Tag':<15} {'Group':<15} {'Limit':<8}")
    print("-" * 70)
    for n in numbers:
        num = n.get("number", "")
        carrier = n.get("carrier", "")
        tag = n.get("tag", "") or "-"
        group = n.get("group", "") or "-"
        limit = n.get("sms_limit", 0)
        limit_str = str(limit) if limit > 0 else "-"
        print(f"{num:<15} {carrier:<12} {tag:<15} {group:<15} {limit_str:<8}")


# =============================================================================
# Reload
# =============================================================================

def reload_all() -> None:
    """Trigger reload of clients and carriers."""
    print("\n=== Reload All ===")
    try:
        resp = post_json("/clients/reload", {})
        if 200 <= resp.status_code < 300:
            print("✅ Clients reloaded.")
        else:
            print(f"❌ Client reload failed ({resp.status_code})")
    except requests.RequestException as e:
        print(f"Network error: {e}")

    try:
        resp = post_json("/carriers/reload", {})
        if 200 <= resp.status_code < 300:
            print("✅ Carriers reloaded.")
        else:
            print(f"❌ Carrier reload failed ({resp.status_code})")
    except requests.RequestException as e:
        print(f"Network error: {e}")


def patch_json(path: str, payload: dict) -> requests.Response:
    """Send PATCH request."""
    url = f"{base_url()}{path}"
    return requests.patch(
        url,
        auth=auth_tuple(),
        headers={"Content-Type": "application/json"},
        data=json.dumps(payload),
        timeout=TIMEOUT,
    )


def delete_json(path: str) -> requests.Response:
    """Send DELETE request."""
    url = f"{base_url()}{path}"
    return requests.delete(
        url,
        auth=auth_tuple(),
        timeout=TIMEOUT,
    )


def change_client_password(identifier: str) -> None:
    """Change client password (by ID or username)."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return
    
    client_id = client.get("id")
    print(f"\n=== Change Password for '{client.get('username')}' (ID: {client_id}) ===")

    new_password = getpass.getpass("New Password (leave blank to auto-generate): ").strip()
    if not new_password:
        new_password = generate_password()
        print("\n🔑 Generated password (save this now; it will not be shown again):")
        print(f"  {new_password}\n")

    confirm = input("Confirm password change? [y/N]: ").strip().lower()
    if confirm != "y":
        print("Cancelled.")
        return

    try:
        resp = patch_json(f"/clients/{client_id}/password", {"new_password": new_password})
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if 200 <= resp.status_code < 300:
        print("✅ Password updated successfully.")
    else:
        print(f"❌ Failed ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)


# =============================================================================
# API Key Operations
# =============================================================================

def list_api_keys(identifier: str) -> Optional[List[Dict[str, Any]]]:
    """List all API keys for a client."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return None

    client_id = client.get("id")
    print(f"\n=== API Keys for '{client.get('username')}' (ID: {client_id}) ===")

    try:
        resp = get_json(f"/clients/{client_id}/api-keys")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code != 200:
        print(f"❌ Failed to list API keys ({resp.status_code})")
        return None

    keys = resp.json()
    if not keys:
        print("No API keys found.")
        return []

    print(f"\n{'ID':<6} {'Name':<20} {'Prefix':<20} {'Scopes':<20} {'Active':<8} {'Expires':<12}")
    print("-" * 90)
    for k in keys:
        kid = str(k.get("id", ""))[:6]
        name = (k.get("name") or "")[:20]
        prefix = (k.get("key_prefix") or "")[:20]
        scopes = (k.get("scopes") or "")[:20]
        active = "✅" if k.get("active", True) else "❌"
        expires = str(k.get("expires_at") or "never")[:12]
        nums = k.get("allowed_numbers") or []
        num_str = f" [{len(nums)} nums]" if nums else ""
        print(f"{kid:<6} {name:<20} {prefix:<20} {scopes:<20} {active:<8} {expires:<12}{num_str}")

    print(f"\nTotal: {len(keys)} keys")
    return keys


def create_api_key_interactive(identifier: str) -> Optional[str]:
    """Interactively create a new API key for a client."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return None

    client_id = client.get("id")
    print(f"\n=== Create API Key for '{client.get('username')}' (ID: {client_id}) ===")

    name = input("Key Name (e.g., 'CSV Import App'): ").strip()
    if not name:
        name = "API Key"

    print("\nScopes (comma-separated):")
    print("  send  - Send individual messages")
    print("  batch - Submit and track batch jobs")
    print("  usage - Read usage statistics")
    scopes = input("Scopes [default: send,batch,usage]: ").strip()
    if not scopes:
        scopes = "send,batch,usage"

    rate_limit_str = input("Rate Limit (requests/min, 0 = use client limit): ").strip()
    try:
        rate_limit = int(rate_limit_str) if rate_limit_str else 0
    except ValueError:
        rate_limit = 0

    expires_str = input("Expires in days (0 = never): ").strip()
    try:
        expires_in_days = int(expires_str) if expires_str else 0
    except ValueError:
        expires_in_days = 0

    # Number scoping
    numbers = client.get("numbers") or []
    allowed_number_ids = []
    if numbers:
        print(f"\nClient has {len(numbers)} numbers:")
        for n in numbers:
            print(f"  ID {n.get('id')}: {n.get('number')} ({n.get('carrier', '')})")
        scope_nums = input("Restrict to number IDs (comma-separated, blank = all): ").strip()
        if scope_nums:
            try:
                allowed_number_ids = [int(x.strip()) for x in scope_nums.split(",") if x.strip()]
            except ValueError:
                print("⚠️ Invalid number IDs, using all numbers.")
                allowed_number_ids = []

    payload = {
        "name": name,
        "scopes": scopes,
        "rate_limit": rate_limit,
        "expires_in_days": expires_in_days,
        "allowed_number_ids": allowed_number_ids,
    }

    try:
        resp = post_json(f"/clients/{client_id}/api-keys", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if 200 <= resp.status_code < 300:
        data = resp.json()
        raw_key = data.get("key", "")
        print(f"\n✅ API Key created: {name}")
        print(f"\n🔑 Raw Key (SAVE NOW — will not be shown again):")
        print(f"  {raw_key}\n")
        print(f"  Prefix:  {data.get('key_prefix')}")
        print(f"  Scopes:  {data.get('scopes')}")
        print(f"  Expires: {data.get('expires_at') or 'never'}")
        return raw_key
    else:
        print(f"❌ Failed to create API key ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)
        return None


def revoke_api_key(identifier: str) -> None:
    """Revoke an API key for a client."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return

    client_id = client.get("id")

    # List keys first
    keys = list_api_keys(identifier)
    if not keys:
        return

    key_id_str = input("\nKey ID to revoke: ").strip()
    if not key_id_str:
        print("Key ID required.")
        return

    confirm = input(f"Revoke key {key_id_str}? This cannot be undone. [y/N]: ").strip().lower()
    if confirm != "y":
        print("Cancelled.")
        return

    try:
        resp = delete_json(f"/clients/{client_id}/api-keys/{key_id_str}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if 200 <= resp.status_code < 300:
        print(f"✅ API key {key_id_str} revoked.")
    else:
        print(f"❌ Failed ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)


# =============================================================================
# Batch Job Operations
# =============================================================================

def list_batch_jobs(identifier: str) -> Optional[List[Dict[str, Any]]]:
    """List recent batch jobs for a client (uses client credentials)."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return None

    # We need client credentials for this endpoint
    username = client.get("username", "")
    print(f"\n=== Batch Jobs for '{username}' ===")
    print("Note: This endpoint requires client credentials (not admin key).")

    client_password = getpass.getpass(f"Client password for '{username}': ").strip()
    if not client_password:
        print("Password required.")
        return None

    try:
        url = f"{base_url()}/messages/batch"
        resp = requests.get(url, auth=(username, client_password), timeout=TIMEOUT)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code != 200:
        print(f"❌ Failed ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)
        return None

    jobs = resp.json()
    if not jobs:
        print("No batch jobs found.")
        return []

    print(f"\n{'ID':<38} {'Status':<18} {'Total':<7} {'Sent':<6} {'Failed':<7} {'Queued':<7} {'Created':<20}")
    print("-" * 110)
    for j in jobs:
        jid = (j.get("id") or "")[:38]
        status = (j.get("status") or "")[:18]
        total = j.get("total_count", 0)
        sent = j.get("sent_count", 0)
        failed = j.get("failed_count", 0)
        queued = j.get("queued_count", 0)
        created = str(j.get("created_at") or "")[:20]
        print(f"{jid:<38} {status:<18} {total:<7} {sent:<6} {failed:<7} {queued:<7} {created:<20}")

    print(f"\nTotal: {len(jobs)} jobs")
    return jobs


def show_batch_job_detail(identifier: str) -> None:
    """Show detail for a specific batch job including per-message status."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return

    username = client.get("username", "")
    job_id = input("Batch Job ID: ").strip()
    if not job_id:
        print("Job ID required.")
        return

    client_password = getpass.getpass(f"Client password for '{username}': ").strip()
    if not client_password:
        print("Password required.")
        return

    # Get job status
    try:
        url = f"{base_url()}/messages/batch/{job_id}"
        resp = requests.get(url, auth=(username, client_password), timeout=TIMEOUT)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        print(f"❌ Job not found ({resp.status_code})")
        return

    job = resp.json()
    print(f"\n=== Batch Job {job_id} ===")
    print(f"  Status:     {job.get('status')}")
    print(f"  Total:      {job.get('total_count', 0)}")
    print(f"  Sent:       {job.get('sent_count', 0)}")
    print(f"  Failed:     {job.get('failed_count', 0)}")
    print(f"  Queued:     {job.get('queued_count', 0)}")
    print(f"  From:       {job.get('from_number')}")
    print(f"  Throttle:   {job.get('throttle_rps', 30)} msg/sec")
    print(f"  Max Retry:  {job.get('max_retry_mins', 60)} min")
    print(f"  Created:    {job.get('created_at')}")

    errors = job.get("errors") or []
    if errors:
        print(f"\n  Errors ({len(errors)}):")
        for e in errors[:10]:
            print(f"    - {e}")
        if len(errors) > 10:
            print(f"    ... and {len(errors) - 10} more")

    # Ask to list messages
    show_msgs = input("\nList individual messages? [y/N]: ").strip().lower()
    if show_msgs == "y":
        status_filter = input("Filter by status (pending/sent/queued/failed/cancelled, blank=all): ").strip()
        try:
            msg_url = f"{base_url()}/messages/batch/{job_id}/messages"
            if status_filter:
                msg_url += f"?status={status_filter}"
            msg_resp = requests.get(msg_url, auth=(username, client_password), timeout=TIMEOUT)
        except requests.RequestException as e:
            print(f"Network error: {e}")
            return

        if msg_resp.status_code == 200:
            items = msg_resp.json()
            print(f"\n  Messages ({len(items)}):")
            print(f"  {'#':<5} {'ID':<38} {'To':<15} {'Status':<12} {'Error':<30}")
            print("  " + "-" * 105)
            for item in items[:50]:
                idx = item.get("index", 0)
                mid = (item.get("id") or "")[:38]
                to = (item.get("to") or "")[:15]
                status = (item.get("status") or "")[:12]
                error = (item.get("error") or "")[:30]
                print(f"  {idx:<5} {mid:<38} {to:<15} {status:<12} {error:<30}")
            if len(items) > 50:
                print(f"  ... and {len(items) - 50} more")


# =============================================================================
# Failover Operations
# =============================================================================

def list_failovers(identifier: str) -> None:
    """List failover entries for a client."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return

    client_id = client.get("id")
    username = client.get("username", "")
    print(f"\n=== Failovers for '{username}' (ID: {client_id}) ===")

    try:
        resp = get_json(f"/clients/{client_id}/failovers")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        print(f"❌ Failed to list failovers ({resp.status_code})")
        return

    failovers = resp.json()
    if not failovers:
        print("No failovers configured.")
        return

    print(f"\n{'ID':<6} {'Fallback Client':<22} {'Username':<18} {'Priority':<10} {'Enabled':<9} {'Online':<8}")
    print("-" * 80)
    for fo in failovers:
        fid = str(fo.get("id", ""))[:6]
        fb_name = (fo.get("fallback_client_name") or "N/A")[:22]
        fb_user = (fo.get("fallback_client_username") or "N/A")[:18]
        priority = str(fo.get("priority", 0))[:10]
        enabled = "✅" if fo.get("enabled", True) else "❌"
        online = "🟢" if fo.get("fallback_online", False) else "🔴"
        print(f"{fid:<6} {fb_name:<22} {fb_user:<18} {priority:<10} {enabled:<9} {online:<8}")

    print(f"\nTotal: {len(failovers)} failovers")


def add_failover_interactive(identifier: str) -> None:
    """Interactively add a failover client."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return

    client_id = client.get("id")
    print(f"\n=== Add Failover for '{client.get('username')}' (ID: {client_id}) ===")

    # List available clients for selection
    print("\nAvailable clients:")
    try:
        resp = get_json("/clients")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        print("❌ Failed to list clients")
        return

    all_clients = resp.json()
    # Filter out the primary client and web-only clients
    eligible = [c for c in all_clients if c.get("id") != client_id and c.get("type", "legacy") == "legacy"]

    if not eligible:
        print("No eligible failover clients available (need other legacy clients).")
        return

    print(f"\n{'#':<4} {'ID':<6} {'Username':<18} {'Name':<22} {'Type':<8}")
    print("-" * 60)
    for i, c in enumerate(eligible, 1):
        cid = str(c.get("id", ""))[:6]
        username = c.get("username", "")[:18]
        name = (c.get("name") or "")[:22]
        ctype = c.get("type", "legacy")[:8]
        print(f"{i:<4} {cid:<6} {username:<18} {name:<22} {ctype:<8}")

    choice_str = input("\nSelect client # (or enter client ID): ").strip()
    if not choice_str:
        print("Selection required.")
        return

    fallback_client_id = None
    if choice_str.isdigit():
        choice_num = int(choice_str)
        if 1 <= choice_num <= len(eligible):
            fallback_client_id = eligible[choice_num - 1].get("id")
        else:
            # Maybe it's a direct client ID
            for c in all_clients:
                if c.get("id") == choice_num:
                    fallback_client_id = choice_num
                    break

    if fallback_client_id is None:
        print("Invalid selection.")
        return

    if fallback_client_id == client_id:
        print("❌ A client cannot be its own failover.")
        return

    priority_str = input("Priority (lower = tried first, default 0): ").strip()
    try:
        priority = int(priority_str) if priority_str else 0
    except ValueError:
        priority = 0

    payload = {
        "fallback_client_id": fallback_client_id,
        "priority": priority,
    }

    try:
        resp = post_json(f"/clients/{client_id}/failovers", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if 200 <= resp.status_code < 300:
        print("✅ Failover added.")
    else:
        print(f"❌ Failed ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)


def remove_failover(identifier: str) -> None:
    """Remove a failover entry."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return

    client_id = client.get("id")

    # List failovers first
    list_failovers(identifier)

    failover_id_str = input("\nFailover ID to remove: ").strip()
    if not failover_id_str:
        print("Failover ID required.")
        return

    confirm = input(f"Remove failover {failover_id_str}? [y/N]: ").strip().lower()
    if confirm != "y":
        print("Cancelled.")
        return

    try:
        resp = delete_json(f"/clients/{client_id}/failovers/{failover_id_str}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if 200 <= resp.status_code < 300:
        print(f"✅ Failover {failover_id_str} removed.")
    else:
        print(f"❌ Failed ({resp.status_code})")
        try:
            print(resp.json())
        except Exception:
            print(resp.text)


def show_smpp_status(identifier: str) -> None:
    """Show SMPP session and failover status for a client."""
    client = get_client_by_identifier(identifier)
    if not client:
        print(f"Client '{identifier}' not found.")
        return

    client_id = client.get("id")
    print(f"\n=== SMPP Status for '{client.get('username')}' (ID: {client_id}) ===")

    try:
        resp = get_json(f"/clients/{client_id}/smpp-status")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        print(f"❌ Failed ({resp.status_code})")
        return

    data = resp.json()
    online = data.get("online", False)
    ip = data.get("ip", "")

    status_icon = "🟢 ONLINE" if online else "🔴 OFFLINE"
    print(f"\n  Primary: {status_icon}")
    if ip:
        print(f"  IP: {ip}")

    failovers = data.get("failovers") or []
    if failovers:
        print(f"\n  Failovers ({len(failovers)}):")
        for fo in failovers:
            fo_status = "🟢" if fo.get("online", False) else "🔴"
            print(f"    {fo_status} {fo.get('username', '')} ({fo.get('name', '')}) - priority {fo.get('priority', 0)}")
    else:
        print("\n  No failovers configured.")


# =============================================================================
# Menu
# =============================================================================

def menu() -> None:
    last_client: Optional[str] = None

    while True:
        print("\n" + "=" * 60)
        print(" GOMSGGW Manager ".center(60, "="))
        print("=" * 60)
        print(f"Base URL: {base_url()}")
        if last_client:
            print(f"Last client: {last_client}")

        print("\n📡 Carriers:")
        print("  1) List carriers")
        print("  2) Add carrier")

        print("\n📋 Clients:")
        print("  3) List clients")
        print("  4) Show client details")
        print("  5) Create client")
        print("  6) Update client settings")
        print("  7) Change client password")

        print("\n📞 Numbers:")
        print("  8) List client numbers")
        print("  9) Add numbers to client")

        print("\n🔑 API Keys:")
        print("  a) List API keys for client")
        print("  b) Create API key for client")
        print("  c) Revoke API key")

        print("\n📦 Batch Jobs:")
        print("  d) List batch jobs for client")
        print("  e) Show batch job detail")

        print("\n🔄 Failover:")
        print("  f) List failovers for client")
        print("  g) Add failover client")
        print("  h) Remove failover")
        print("  i) SMPP session status")

        print("\n⚙️ Admin:")
        print("  r) Reload all (clients + carriers)")
        print("  q) Quick flow: create client → add numbers → reload")

        print("\n  0) Exit")

        choice = input("\n> ").strip().lower()

        if choice == "1":
            list_carriers()

        elif choice == "2":
            create_carrier_interactive()

        elif choice == "3":
            list_clients()

        elif choice == "4":
            username = input("Client username: ").strip() or last_client
            if username:
                show_client_details(username)
            else:
                print("Username required.")

        elif choice == "5":
            created = create_client_interactive()
            if created:
                last_client = created

        elif choice == "6":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                update_client_settings(identifier)
            else:
                print("Client ID or username required.")

        elif choice == "7":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                change_client_password(identifier)
            else:
                print("Client ID or username required.")

        elif choice == "8":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                list_client_numbers(identifier)
            else:
                print("Client ID or username required.")

        elif choice == "9":
            identifier = input("Client ID or username: ").strip() or last_client
            if not identifier:
                print("Client ID or username required.")
                continue
            
            # Show available carriers
            print("\nAvailable carriers (from gateway):")
            carriers = list_carriers()
            carrier_names = [c.get("name", "") for c in (carriers or [])]
            
            carrier = input("Carrier name (default: telnyx): ").strip() or "telnyx"
            print("Enter numbers (comma-separated or one per line, E.164 OK e.g. +12505551234).")
            print("Press ENTER on an empty line to finish (or Ctrl+D).")
            try:
                lines = []
                while True:
                    line = input()
                    if not line:
                        break
                    lines.append(line)
            except EOFError:
                pass
            raw = ",".join(lines)
            nums = parse_numbers_csv(raw)
            if nums:
                add_numbers_to_client(identifier, nums, carrier=carrier)
                last_client = identifier
            else:
                print("No valid numbers provided.")

        # --- API Key Operations ---
        elif choice == "a":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                list_api_keys(identifier)
                last_client = identifier
            else:
                print("Client ID or username required.")

        elif choice == "b":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                create_api_key_interactive(identifier)
                last_client = identifier
            else:
                print("Client ID or username required.")

        elif choice == "c":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                revoke_api_key(identifier)
                last_client = identifier
            else:
                print("Client ID or username required.")

        # --- Batch Job Operations ---
        elif choice == "d":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                list_batch_jobs(identifier)
                last_client = identifier
            else:
                print("Client ID or username required.")

        elif choice == "e":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                show_batch_job_detail(identifier)
                last_client = identifier
            else:
                print("Client ID or username required.")

        # --- Failover Operations ---
        elif choice == "f":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                list_failovers(identifier)
                last_client = identifier
            else:
                print("Client ID or username required.")

        elif choice == "g":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                add_failover_interactive(identifier)
                last_client = identifier
            else:
                print("Client ID or username required.")

        elif choice == "h":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                remove_failover(identifier)
                last_client = identifier
            else:
                print("Client ID or username required.")

        elif choice == "i":
            identifier = input("Client ID or username: ").strip() or last_client
            if identifier:
                show_smpp_status(identifier)
                last_client = identifier
            else:
                print("Client ID or username required.")

        elif choice == "r":
            reload_all()

        elif choice == "q":
            # Quick flow
            username = create_client_interactive()
            if not username:
                continue
            last_client = username

            # Show carriers
            print("\nAvailable carriers:")
            list_carriers()
            
            carrier = input("Carrier name (default: telnyx): ").strip() or "telnyx"
            print("Enter numbers (comma-separated or one per line, E.164 OK e.g. +12505551234).")
            print("Press ENTER on an empty line to finish (or Ctrl+D).")
            try:
                lines = []
                while True:
                    line = input()
                    if not line:
                        break
                    lines.append(line)
            except EOFError:
                pass
            raw = ",".join(lines)
            nums = parse_numbers_csv(raw)
            if nums:
                add_numbers_to_client(username, nums, carrier=carrier)
            else:
                print("No numbers provided; skipping.")

            if input("Reload all? [Y/n]: ").strip().lower() != "n":
                reload_all()

        elif choice == "0":
            print("Bye!")
            break

        else:
            print("Invalid choice.")


def main():
    try:
        menu()
    except KeyboardInterrupt:
        print("\nInterrupted. Bye!")


if __name__ == "__main__":
    main()