class Shop:
    def total(self, items, discount=0, *rest, **opts):
        """Docstrings are not statements."""
        result = 0
        for item in items:
            if item.price > 100 and item.qty:
                result += item.price
            elif item.price > 50:
                result += item.price / 2
            else:
                try:
                    result += int(item.price)
                except ValueError:
                    pass
        values = [v for v in items if v]
        return result if result > 0 else discount


def outer(a):
    def inner(b):
        return b * 2

    return inner(a)


def kind(x):
    match x:
        case 1:
            return "one"
        case [a, b]:
            return "pair"
        case _:
            return "other"
