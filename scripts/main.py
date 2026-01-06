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
        num_count = len(c.get("numbers", []))
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
    numbers = client.get("numbers", [])
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
    return [n.get("number", "") for n in client.get("numbers", [])]


def normalize_number(num: str) -> str:
    """Normalize a phone number to digits only."""
    return re.sub(r"[^\d]", "", num)


def parse_numbers_csv(raw: str) -> List[str]:
    """Parse comma-separated numbers, validating format."""
    parts = [p.strip() for p in raw.replace("\n", ",").split(",") if p.strip()]
    valid, invalid = [], []
    for p in parts:
        normalized = normalize_number(p)
        if len(normalized) >= 10 and len(normalized) <= 11:
            # Ensure 11 digits (add 1 if 10)
            if len(normalized) == 10:
                normalized = "1" + normalized
            valid.append(normalized)
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
        existing = [n.get("number", "") for n in client.get("numbers", [])]
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

    numbers = client.get("numbers", [])
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
            print("Enter numbers (comma-separated or one per line, Ctrl+D when done):")
            try:
                lines = []
                while True:
                    line = input()
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
            raw = input("Comma-separated numbers: ").strip()
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