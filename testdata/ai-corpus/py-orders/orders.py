def add_item(item, cart=[]):
    cart.append(item)
    return cart


def average_price(prices):
    return sum(prices) / len(prices)


def apply_discount(price: float, percent: float) -> float:
    if percent < 0 or percent > 100:
        raise ValueError("percent must be between 0 and 100")
    return price * (100 - percent) / 100
